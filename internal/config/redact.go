package config

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Normalized returns a deterministic redacted projection of the
// configuration for `config show` and diagnostics: keys are sorted, secret
// values never appear because references are stored unresolved and printed
// as their reference identifiers only (SEC-006, SEC-007), and behavior
// sections are normalized with sorted pattern and skills lists.
func (c *Config) Normalized() ([]byte, error) {
	out := normalizedConfig{
		Version:   c.Version,
		Instance:  c.Instance,
		Limits:    c.Limits,
		Resources: normalizedResources(c.Resources),
		Targets:   normalizedTargets(c.Targets),
		Routes:    normalizedRoutes(c.Routes),
		Retention: c.Retention,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type normalizedConfig struct {
	Version   int                         `json:"version"`
	Instance  Instance                    `json:"instance"`
	Limits    Limits                      `json:"limits"`
	Resources map[string]Resource         `json:"resources"`
	Targets   map[string]normalizedTarget `json:"targets"`
	Routes    map[string]normalizedRoute  `json:"routes"`
	Retention *Retention                  `json:"retention,omitempty"`
}

type normalizedTarget struct {
	Type                 string   `json:"type"`
	Executable           string   `json:"executable,omitempty"`
	CapabilityReport     string   `json:"capability_report,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	SubmitTimeout        string   `json:"submit_timeout,omitempty"`
	LookupTimeout        string   `json:"lookup_timeout,omitempty"`
	EnvironmentAllowlist []string `json:"environment_allowlist,omitempty"`
	Endpoint             string   `json:"endpoint,omitempty"`
	// Auth prints only the secret reference identifier; the value is never
	// resolved into this projection.
	Auth              *normalizedAuth `json:"auth,omitempty"`
	IdempotencyHeader string          `json:"idempotency_header,omitempty"`
}

type normalizedAuth struct {
	Type       string `json:"type"`
	SecretRef  string `json:"secret_ref"`
	HeaderName string `json:"header_name,omitempty"`
}

type normalizedRoute struct {
	Enabled        bool           `json:"enabled"`
	Source         Source         `json:"source"`
	Batching       Batching       `json:"batching"`
	Policy         Policy         `json:"policy"`
	Dispatch       Dispatch       `json:"dispatch"`
	Reconciliation Reconciliation `json:"reconciliation"`
	Retention      *Retention     `json:"retention,omitempty"`
}

func normalizedResources(in map[string]Resource) map[string]Resource {
	out := make(map[string]Resource, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizedTargets(in map[string]Target) map[string]normalizedTarget {
	out := make(map[string]normalizedTarget, len(in))
	for k, v := range in {
		n := normalizedTarget{
			Type:                 v.Type,
			Executable:           v.Executable,
			CapabilityReport:     v.CapabilityReport,
			RequiredCapabilities: sortedCopy(v.RequiredCapabilities),
			SubmitTimeout:        v.SubmitTimeout,
			LookupTimeout:        v.LookupTimeout,
			EnvironmentAllowlist: sortedCopy(v.EnvironmentAllowlist),
			Endpoint:             v.Endpoint,
			IdempotencyHeader:    v.IdempotencyHeader,
		}
		if v.Auth != nil {
			n.Auth = &normalizedAuth{
				Type:       v.Auth.Type,
				SecretRef:  v.Auth.SecretRef,
				HeaderName: v.Auth.HeaderName,
			}
		}
		out[k] = n
	}
	return out
}

func normalizedRoutes(in map[string]Route) map[string]normalizedRoute {
	out := make(map[string]normalizedRoute, len(in))
	for k, v := range in {
		v.Source.Include = sortedCopy(v.Source.Include)
		v.Source.Exclude = sortedCopy(v.Source.Exclude)
		v.Policy.Protected = sortedCopy(v.Policy.Protected)
		v.Policy.Immutable = sortedCopy(v.Policy.Immutable)
		v.Dispatch.Skills = sortedCopy(v.Dispatch.Skills)
		out[k] = normalizedRoute(v)
	}
	return out
}

// SortedRouteIDs returns route IDs in deterministic order for iteration.
func (c *Config) SortedRouteIDs() []string {
	return sortedKeys(c.Routes)
}

func sortedKeys(m map[string]Route) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

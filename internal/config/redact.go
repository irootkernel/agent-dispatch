package config

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// Normalized returns a deterministic redacted projection of the
// configuration for `config show` and diagnostics: keys are sorted, secret
// values never appear because references are stored unresolved and printed
// as their reference identifiers only (SEC-006, SEC-007), behavior
// sections are normalized with sorted pattern, skill, and destination
// lists (FAN-012: order is never semantic).
func (c *Config) Normalized() ([]byte, error) {
	out := normalizedConfig{
		Version:       c.Version,
		Instance:      c.Instance,
		Limits:        c.Limits,
		Resources:     normalizedResources(c.Resources),
		Targets:       normalizedTargets(c.Targets),
		HermesTargets: normalizedHermesTargets(c.HermesTargets),
		Routes:        normalizedRoutes(c.Routes),
		Retention:     c.Retention,
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
	Version       int                               `json:"version"`
	Instance      Instance                          `json:"instance"`
	Limits        Limits                            `json:"limits"`
	Resources     map[string]Resource               `json:"resources"`
	Targets       map[string]normalizedTarget       `json:"targets"`
	HermesTargets map[string]normalizedHermesTarget `json:"hermes_targets"`
	Routes        map[string]normalizedRoute        `json:"routes"`
	Retention     *Retention                        `json:"retention,omitempty"`
}

type normalizedTarget struct {
	Type string `json:"type"`
	// Endpoint prints with its query string masked: webhook tokens ride
	// in query parameters, and config show echoed them verbatim (review
	// M-20, E8 correction).
	Endpoint          string          `json:"endpoint,omitempty"`
	SubmitTimeout     string          `json:"submit_timeout,omitempty"`
	Auth              *normalizedAuth `json:"auth,omitempty"`
	IdempotencyHeader string          `json:"idempotency_header,omitempty"`
}

type normalizedHermesTarget struct {
	Board                string   `json:"board"`
	Executable           string   `json:"executable,omitempty"`
	MinimumVersion       string   `json:"minimum_version,omitempty"`
	Compatibility        string   `json:"compatibility"`
	SubmitTimeout        string   `json:"submit_timeout,omitempty"`
	LookupTimeout        string   `json:"lookup_timeout,omitempty"`
	EnvironmentAllowlist []string `json:"environment_allowlist,omitempty"`
}

type normalizedAuth struct {
	Type       string `json:"type"`
	SecretRef  string `json:"secret_ref"`
	HeaderName string `json:"header_name,omitempty"`
}

type normalizedDestination struct {
	ID             string         `json:"id"`
	Target         string         `json:"target"`
	Profile        string         `json:"profile"`
	Skills         []string       `json:"skills"`
	Workstream     string         `json:"workstream"`
	Workspace      string         `json:"workspace,omitempty"`
	MutexKey       string         `json:"mutex_key,omitempty"`
	ExecutionHints ExecutionHints `json:"execution_hints"`
	Conditions     *Conditions    `json:"conditions,omitempty"`
}

type normalizedNotifications struct {
	Events []string            `json:"events"`
	Sinks  []normalizedSinkRef `json:"sinks"`
}

type normalizedSinkRef struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Endpoint string          `json:"endpoint,omitempty"`
	Auth     *normalizedAuth `json:"auth,omitempty"`
}

type normalizedRoute struct {
	Enabled          bool                     `json:"enabled"`
	Source           Source                   `json:"source"`
	Batching         Batching                 `json:"batching"`
	Policy           Policy                   `json:"policy"`
	FanoutMode       string                   `json:"fanout_mode"`
	Destinations     []normalizedDestination  `json:"destinations"`
	Notifications    *normalizedNotifications `json:"notifications,omitempty"`
	SubmissionRetry  Retry                    `json:"submission_retry"`
	LatestState      bool                     `json:"latest_state"`
	FailureBudget    int                      `json:"failure_budget"`
	ActiveStaleAfter string                   `json:"active_stale_after"`
	Reconciliation   Reconciliation           `json:"reconciliation"`
	Retention        *Retention               `json:"retention,omitempty"`
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
			Type:              v.Type,
			Endpoint:          maskEndpointQuery(v.Endpoint),
			SubmitTimeout:     v.SubmitTimeout,
			IdempotencyHeader: v.IdempotencyHeader,
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

func normalizedHermesTargets(in map[string]HermesTarget) map[string]normalizedHermesTarget {
	out := make(map[string]normalizedHermesTarget, len(in))
	for k, v := range in {
		out[k] = normalizedHermesTarget{
			Board:                v.Board,
			Executable:           v.Executable,
			MinimumVersion:       v.MinimumVersion,
			Compatibility:        v.Compatibility,
			SubmitTimeout:        v.SubmitTimeout,
			LookupTimeout:        v.LookupTimeout,
			EnvironmentAllowlist: sortedCopy(v.EnvironmentAllowlist),
		}
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
		dests := make([]normalizedDestination, 0, len(v.Destinations))
		for _, d := range v.SortedDestinations() {
			d.Skills = sortedCopy(d.Skills)
			if d.Conditions != nil {
				d.Conditions.PathInclude = sortedCopy(d.Conditions.PathInclude)
				d.Conditions.PathExclude = sortedCopy(d.Conditions.PathExclude)
				d.Conditions.Operations = sortedCopy(d.Conditions.Operations)
				d.Conditions.Classifications = sortedCopy(d.Conditions.Classifications)
				d.Conditions.PolicyOutcomes = sortedCopy(d.Conditions.PolicyOutcomes)
			}
			dests = append(dests, normalizedDestination(d))
		}
		var notifications *normalizedNotifications
		if v.Notifications != nil {
			sinks := make([]normalizedSinkRef, 0, len(v.Notifications.Sinks))
			sorted := append([]NotificationSink(nil), v.Notifications.Sinks...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
			for _, sink := range sorted {
				ref := normalizedSinkRef{
					ID:       sink.ID,
					Type:     sink.Type,
					Endpoint: maskEndpointQuery(sink.Endpoint),
				}
				if sink.Auth != nil {
					ref.Auth = &normalizedAuth{
						Type:       sink.Auth.Type,
						SecretRef:  sink.Auth.SecretRef,
						HeaderName: sink.Auth.HeaderName,
					}
				}
				sinks = append(sinks, ref)
			}
			notifications = &normalizedNotifications{
				Events: sortedCopy(v.Notifications.Events),
				Sinks:  sinks,
			}
		}
		out[k] = normalizedRoute{
			Enabled: v.Enabled, Source: v.Source, Batching: v.Batching, Policy: v.Policy,
			FanoutMode: v.FanoutMode, Destinations: dests, Notifications: notifications,
			SubmissionRetry: v.SubmissionRetry, LatestState: v.LatestState,
			FailureBudget: v.FailureBudget, ActiveStaleAfter: v.ActiveStaleAfter,
			Reconciliation: v.Reconciliation, Retention: v.Retention,
		}
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

// maskEndpointQuery replaces every query parameter's value with
// [redacted] while keeping the parameter names visible for operators.
func maskEndpointQuery(endpoint string) string {
	i := strings.IndexByte(endpoint, '?')
	if i < 0 {
		return endpoint
	}
	base, query := endpoint[:i], endpoint[i+1:]
	parts := strings.Split(query, "&")
	for j, p := range parts {
		if k := strings.IndexByte(p, '='); k >= 0 {
			parts[j] = p[:k+1] + "[redacted]"
		}
	}
	return base + "?" + strings.Join(parts, "&")
}

package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"sort"
)

// CaseMode resolves the v0.1 default `filesystem` case policy for this
// host: macOS path lookup is case-insensitive, Linux case-sensitive. The
// pattern engine consumes this resolved value so behavior is explicit
// and identical across hosts for the same resolved mode.
func CaseMode() string {
	if runtime.GOOS == "darwin" {
		return "insensitive"
	}
	return "sensitive"
}

// RouteRevision computes the deterministic behavior-affecting revision of
// one route (configuration-spec §13, POL-007). The canonical projection
// covers the source binding, resource ID, normalized patterns, batch
// limits, policy actions, target ID, profile, skills, mutex, latest-state
// flag, submission retry, execution hints, failure budget, the
// active-stale bound, and the referenced target's capability requirements; it excludes comments,
// display order, the state directory, log level, and secret values. Map
// iteration order is neutralized by sorting, so the same behavior yields
// the same revision on every run and platform.
func RouteRevision(cfg *Config, routeID string) (string, bool) {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return "", false
	}
	target := cfg.Targets[route.Dispatch.Target]
	projection := map[string]any{
		"source": map[string]any{
			"type":         route.Source.Type,
			"source_id":    route.Source.SourceID,
			"resource":     route.Source.Resource,
			"trigger_name": route.Source.TriggerName,
			"include":      sortedCopy(route.Source.Include),
			"exclude":      sortedCopy(route.Source.Exclude),
			// The resolved pattern case mode is behavior-affecting
			// (configuration-spec §7: v0.1 default `filesystem`) and is
			// recorded so plans classified under different modes cannot
			// share a revision.
			"case_mode": CaseMode(),
		},
		"batching": route.Batching,
		"policy": map[string]any{
			"protected":             sortedCopy(route.Policy.Protected),
			"immutable":             sortedCopy(route.Policy.Immutable),
			"bulk_action":           route.Policy.BulkAction,
			"overflow_action":       route.Policy.OverflowAction,
			"fresh_instance_action": route.Policy.FreshInstanceAction,
			"unsafe_path_action":    route.Policy.UnsafePathAction,
		},
		"dispatch": map[string]any{
			"target":             route.Dispatch.Target,
			"profile":            route.Dispatch.Profile,
			"skills":             sortedCopy(route.Dispatch.Skills),
			"mutex_key":          route.Dispatch.MutexKey,
			"latest_state":       route.Dispatch.LatestState,
			"submission_retry":   route.Dispatch.SubmissionRetry,
			"execution_hints":    route.Dispatch.ExecutionHints,
			"failure_budget":     route.Dispatch.FailureBudget,
			"active_stale_after": route.Dispatch.ActiveStaleAfter,
		},
		"required_capabilities": sortedCopy(target.RequiredCapabilities),
	}
	// Batching and the retry structs marshal through fixed field order in
	// their struct tags, which encoding/json keeps stable.
	enc, err := json.Marshal(projection)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(enc)
	return hex.EncodeToString(sum[:]), true
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

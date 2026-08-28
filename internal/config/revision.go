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
// one route (configuration-spec §13, POL-007, FAN-012). Since the v0.1.5
// cutover the canonical projection covers the source binding, resource
// ID, normalized patterns, batch limits, policy actions, the fan-out
// mode, the sorted destination set with each destination revision
// (E11-T1), the declared notification policy and sink references, the
// route-level runtime envelope (submission retry, latest-state flag,
// failure budget, active-stale bound), the referenced resource's shape,
// the global limits block, and every referenced target's shape and
// transport bounds (E8-T3, E9-T3, E9-T6): repointing a vault, moving a
// board or endpoint, swapping the target binary, changing its execution
// bounds, or changing how compatibility is proven must pause an
// acknowledged route. The route's reconciliation block joins the
// projection, while the retention block stays out by explicit
// disposition (OPS-003). The projection excludes comments, display
// order, the state directory, log level, and secret values — a secret
// REFERENCE is behavior-affecting and joins; the resolved secret never
// does. Destination map order is non-semantic: destinations and their
// lists are sorted (FAN-012), so the same behavior yields the same
// revision on every run and platform.
func RouteRevision(cfg *Config, routeID string) (string, bool) {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return "", false
	}
	resource := cfg.Resources[route.Source.Resource]
	gitMode := ""
	if resource.Git != nil {
		gitMode = resource.Git.Mode
	}
	destinations := make([]map[string]any, 0, len(route.Destinations))
	for _, dest := range route.SortedDestinations() {
		destinations = append(destinations, destinationProjection(cfg, dest))
	}
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
		"fanout_mode":   route.FanoutMode,
		"destinations":  destinations,
		"notifications": notificationsProjection(route.Notifications),
		"runtime": map[string]any{
			"submission_retry":   route.SubmissionRetry,
			"latest_state":       route.LatestState,
			"failure_budget":     route.FailureBudget,
			"active_stale_after": route.ActiveStaleAfter,
		},
		// The referenced resource's shape is behavior-affecting (E8-T3,
		// H-2/POL-007): repointing the vault root, switching the file scope
		// or git mode, or changing the global limits must change the
		// revision — and with it the idempotency key — so distinct vaults
		// can never collide and a behavior change pauses the acknowledged
		// route.
		"resource": map[string]any{
			"root":       resource.Root,
			"file_scope": resource.FileScope,
			"git_mode":   gitMode,
		},
		"limits":         cfg.Limits,
		"targets":        routeTargetsProjection(cfg, route),
		"reconciliation": route.Reconciliation,
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

// destinationProjection renders one destination's behavior-affecting
// shape: its target binding, profile, sorted skills, workstream,
// workspace, mutex, execution hints, and sorted selection conditions
// (§14: destination revision includes target, profile, skills,
// workstream, workspace, mutex, hints, and conditions).
func destinationProjection(cfg *Config, dest Destination) map[string]any {
	out := map[string]any{
		"id":              dest.ID,
		"target":          dest.Target,
		"profile":         dest.Profile,
		"skills":          sortedCopy(dest.Skills),
		"workstream":      dest.Workstream,
		"workspace":       dest.Workspace,
		"mutex_key":       dest.MutexKey,
		"execution_hints": dest.ExecutionHints,
	}
	if dest.Conditions != nil {
		out["conditions"] = map[string]any{
			"path_include":    sortedCopy(dest.Conditions.PathInclude),
			"path_exclude":    sortedCopy(dest.Conditions.PathExclude),
			"operations":      sortedCopy(dest.Conditions.Operations),
			"classifications": sortedCopy(dest.Conditions.Classifications),
			"policy_outcomes": sortedCopy(dest.Conditions.PolicyOutcomes),
		}
	} else {
		out["conditions"] = nil
	}
	return out
}

// routeTargetsProjection renders the shape and transport bounds of every
// distinct target the route's destinations reference, keyed by target ID
// and sorted for determinism. Hermes transport fields carry the
// executable, execution bounds, and the probed-compatibility contract
// (minimum_version, compatibility) that replaced the operator-authored
// capability report (E11-T1); webhook transport carries the delivery
// evidence surface (endpoint, authentication reference shape,
// idempotency header).
func routeTargetsProjection(cfg *Config, route Route) map[string]any {
	ids := make([]string, 0, len(route.Destinations))
	for _, dest := range route.Destinations {
		ids = append(ids, dest.Target)
	}
	sort.Strings(ids)
	out := make(map[string]any, len(ids))
	last := ""
	for _, id := range ids {
		if id == last {
			continue
		}
		last = id
		resolved, ok := cfg.ResolveTarget(id)
		if !ok {
			out[id] = map[string]any{"type": "unknown"}
			continue
		}
		if resolved.Hermes != nil {
			t := resolved.Hermes
			minimum := t.MinimumVersion
			if minimum == "" {
				minimum = MinimumEligibleHermesVersion
			}
			out[id] = map[string]any{
				"type": "hermes-kanban",
				// The board binding is behavior-affecting (E8-T3): moving a
				// board changes the idempotency key.
				"board": t.Board,
				"transport": map[string]any{
					"executable":            t.Executable,
					"submit_timeout":        t.SubmitTimeout,
					"environment_allowlist": sortedCopy(t.EnvironmentAllowlist),
					"lookup_timeout":        t.LookupTimeout,
					// How compatibility is proven gates enablement and
					// submission (E9-T6 posture under the E11-T1 contract).
					"minimum_version": minimum,
					"compatibility":   t.Compatibility,
				},
			}
			continue
		}
		t := resolved.Webhook
		out[id] = map[string]any{
			"type": t.Type,
			"transport": map[string]any{
				// The digest commits to the FULL endpoint through a
				// one-way commitment, never the redacted display form: a
				// query-string change (where webhook tokens ride) is
				// behavior-affecting and must pause the acknowledged
				// route, while the masked value stays for printed output
				// only.
				"endpoint_commitment":   endpointCommitment(t.Endpoint),
				"submit_timeout":        t.SubmitTimeout,
				"idempotency_header":    t.IdempotencyHeader,
				"required_capabilities": sortedCopy(t.RequiredCapabilities),
				"auth":                  authProjection(t.Auth),
			},
		}
	}
	return out
}

// endpointCommitment renders a non-reversible digest of the full
// endpoint URL for the revision projection: the same bytes always
// commit identically and the raw value never joins a printed surface.
func endpointCommitment(endpoint string) string {
	sum := sha256.Sum256([]byte("agent-dispatch:endpoint:v1:" + endpoint))
	return hex.EncodeToString(sum[:])
}

// notificationsProjection renders the declared notification policy and
// sink references (§14): the sorted event list and each sink's id, type,
// endpoint, and authentication REFERENCE (never the resolved secret).
// An absent block and an explicitly empty one hash identically.
func notificationsProjection(n *Notifications) map[string]any {
	shape := map[string]any{"events": []string{}, "sinks": []map[string]any{}}
	if n == nil {
		return shape
	}
	shape["events"] = sortedCopy(n.Events)
	sinks := make([]map[string]any, 0, len(n.Sinks))
	sorted := append([]NotificationSink(nil), n.Sinks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, sink := range sorted {
		sinks = append(sinks, map[string]any{
			"id":                  sink.ID,
			"type":                sink.Type,
			"endpoint_commitment": endpointCommitment(sink.Endpoint),
			"auth":                authProjection(sink.Auth),
		})
	}
	shape["sinks"] = sinks
	return shape
}

// DestinationRevision computes the deterministic revision of one
// destination as the route projection sees it (§14): the same projection
// the route revision digests for this destination, hashed alone, so a
// destination-qualified edit can report its own revision and pause state
// (used by the E11-T3 mutation commands).
func DestinationRevision(cfg *Config, route Route, dest Destination) string {
	enc, err := json.Marshal(destinationProjection(cfg, dest))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(enc)
	return "dst-" + hex.EncodeToString(sum[:])
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

// authProjection renders the authentication reference for the revision
// digest: an absent auth block and an explicitly empty one must hash
// identically, and only the reference members (never the resolved
// secret) join the projection.
func authProjection(auth *Auth) map[string]any {
	shape := map[string]any{"type": "", "secret_ref": "", "header_name": ""}
	if auth == nil {
		return shape
	}
	shape["type"] = auth.Type
	shape["secret_ref"] = auth.SecretRef
	shape["header_name"] = auth.HeaderName
	return shape
}

// PolicyRevision computes the deterministic digest of exactly the
// policy-evaluation surface of one route (E9-T3, L-18): the pattern
// sets and resolved case mode the engine classifies under, the batching
// thresholds the planner budgets with, and the structural actions the
// planner chooses between. Every policy decision records it beside the
// route revision so an audit can tell which policy content — not just
// which route declaration — produced a disposition: two revisions of a
// route with identical policy share a policy revision, and one policy
// edit inside an unchanged route revision still moves it. Transport,
// target, and destination-envelope fields cannot change a decision and
// stay out; inert keys (unsafe_path_action) join only when they become
// behavior-affecting.
func PolicyRevision(route Route) string {
	projection := map[string]any{
		"case_mode": CaseMode(),
		"include":   sortedCopy(route.Source.Include),
		"exclude":   sortedCopy(route.Source.Exclude),
		"batching": map[string]any{
			"automatic_threshold": route.Batching.AutomaticThreshold,
			"hard_limit":          route.Batching.HardLimit,
		},
		"policy": map[string]any{
			"protected":             sortedCopy(route.Policy.Protected),
			"immutable":             sortedCopy(route.Policy.Immutable),
			"bulk_action":           route.Policy.BulkAction,
			"overflow_action":       route.Policy.OverflowAction,
			"fresh_instance_action": route.Policy.FreshInstanceAction,
		},
	}
	enc, err := json.Marshal(projection)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(enc)
	return "pol-" + hex.EncodeToString(sum[:12])
}

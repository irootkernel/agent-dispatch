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
// active-stale bound, the referenced target's capability requirements,
// the referenced resource's root/file scope/git mode, the global limits
// block, the target's type/board/endpoint (E8-T3), the transport bounds
// (executable, submit timeout, environment allowlist, manifest byte
// bound; E9-T3), and the delivery-evidence surface (authentication type,
// secret reference, auth header name, idempotency header, lookup
// timeout, capability-report path; E9-T6): changing how a dispatch
// authenticates or deduplicates must pause an acknowledged route like
// any other behavior change. The route's reconciliation block joins the
// projection (its flags change which arrivals produce work), while the
// retention block stays out by explicit disposition: it bounds record
// pruning (OPS-003) and never changes what a dispatch submits or how a
// plan is classified. The projection excludes comments, display order,
// the state directory, log level, and secret values — a secret
// REFERENCE is behavior-affecting and joins; the resolved secret never
// does. Map iteration order is neutralized by sorting, so the same
// behavior yields the same revision on every run and platform.
func RouteRevision(cfg *Config, routeID string) (string, bool) {
	route, ok := cfg.Routes[routeID]
	if !ok {
		return "", false
	}
	target := cfg.Targets[route.Dispatch.Target]
	resource := cfg.Resources[route.Source.Resource]
	gitMode := ""
	if resource.Git != nil {
		gitMode = resource.Git.Mode
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
		// The referenced resource's shape and the target's binding are
		// behavior-affecting (E8-T3, H-2/POL-007): repointing the vault
		// root, switching the file scope or git mode, changing the global
		// limits, or moving the board/endpoint must change the revision —
		// and with it the idempotency key — so distinct vaults can never
		// collide and a behavior change pauses the acknowledged route.
		"resource": map[string]any{
			"root":       resource.Root,
			"file_scope": resource.FileScope,
			"git_mode":   gitMode,
		},
		"limits":       cfg.Limits,
		"target_shape": map[string]any{"type": target.Type, "board": target.Board, "endpoint": target.Endpoint},
		// Transport fields (E9-T3/T3-F006): swapping the target binary,
		// its execution bounds, or its manifest byte bound is
		// behavior-affecting — a route acknowledged under the old
		// transport pauses until re-acknowledged.
		"transport": map[string]any{
			"executable":            target.Executable,
			"submit_timeout":        target.SubmitTimeout,
			"environment_allowlist": sortedCopy(target.EnvironmentAllowlist),
			"max_manifest_bytes":    route.Batching.MaxManifestBytes,
			// Delivery-evidence surface (E9-T6, D-023 F1): the lookup
			// bound and the capability-evidence path gate the enable
			// and reconciliation surfaces, so moving either must pause
			// the acknowledged route.
			"lookup_timeout":     target.LookupTimeout,
			"capability_report":  target.CapabilityReport,
			"idempotency_header": target.IdempotencyHeader,
			// The authentication shape decides which header carries the
			// secret and the deduplication key on the wire. The secret
			// REFERENCE joins (repointing it changes the credential in
			// use); the resolved secret value never does (SEC-006).
			"auth": authProjection(target.Auth),
		},
		// The reconciliation flags change which arrivals produce work
		// (the initial sweep and the daily-expected window), so they are
		// behavior-affecting (E9-T6). The retention block stays out by
		// the documented disposition: pruning bounds never change
		// submission behavior.
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
// target, and dispatch-envelope fields cannot change a decision and
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

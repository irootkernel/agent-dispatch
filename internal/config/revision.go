package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"sort"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
	"golang.org/x/text/unicode/norm"
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

const syncContractVersion = "agent-dispatch.sync-contract/v1"

// SyncContractDigest is the interoperable identity of the v1 sync contract.
// Semantic contract changes require a new version instead of depending on a
// repository-local artifact layout.
func SyncContractDigest() string {
	return digestJSON(syncContractVersion, map[string]string{"schema_version": syncContractVersion})
}

// RemoteRepositoryDigest canonicalizes one credential-free Git remote URI and
// returns the sync-contract repository identity plus its canonical form.
func RemoteRepositoryDigest(raw string) (string, string, error) {
	if strings.Contains(raw, "%") {
		return "", "", fmt.Errorf("remote repository URI must not use percent encoding")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", "", fmt.Errorf("remote repository URI must be absolute")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "ssh" {
		return "", "", fmt.Errorf("remote repository URI scheme must be https or ssh")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("remote repository URI must not contain a query or fragment")
	}
	username := ""
	if scheme == "https" {
		if u.User != nil {
			return "", "", fmt.Errorf("https remote repository URI must not contain userinfo")
		}
	} else {
		if u.User == nil || u.User.Username() == "" {
			return "", "", fmt.Errorf("ssh remote repository URI requires a username")
		}
		if _, present := u.User.Password(); present {
			return "", "", fmt.Errorf("ssh remote repository URI must not contain a password")
		}
		username = u.User.Username() + "@"
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", "", fmt.Errorf("remote repository URI requires a host")
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "ssh" && port == "22") {
		port = ""
	}
	if port != "" {
		return "", "", fmt.Errorf("remote repository URI must not use a non-default port")
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	path := norm.NFC.String(u.Path)
	if path == "" || path == "/" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", "", fmt.Errorf("remote repository URI requires exactly one leading slash and a repository path")
	}
	path = strings.TrimSuffix(path, "/")
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", "", fmt.Errorf("remote repository URI path must not contain empty or dot segments")
		}
	}
	path = strings.TrimSuffix(path, ".git")
	if path == "" || path == "/" {
		return "", "", fmt.Errorf("remote repository URI requires a repository path")
	}
	canonical := scheme + "://" + username + host + path
	return digestBytes("agent-dispatch.remote-repository/v1", []byte(canonical)), canonical, nil
}

// SyncRevision is the normalized cooperative-import acknowledgement input.
// Membership roster, endpoint, publisher-key, and peer-credential movement is
// deliberately excluded: SYN-009 makes those changes invalidate verification
// targets, not an acknowledgement accepted under the same administrator trust
// policy. Protected effects revalidate current membership independently.
func SyncRevision(cfg *Config) (string, bool) {
	if cfg.Sync == nil {
		return "", false
	}
	s := cfg.Sync
	resource := cfg.Resources[s.Resource]
	projection := struct {
		SchemaVersion          string   `json:"schema_version"`
		GroupID                string   `json:"group_id"`
		ResourceID             string   `json:"resource_id"`
		Resource               Resource `json:"resource"`
		RemoteName             string   `json:"remote_name"`
		RemoteRepositoryDigest string   `json:"remote_repository_digest"`
		ContentRef             string   `json:"content_ref"`
		MembershipRef          string   `json:"membership_ref"`
		LocalInstanceID        string   `json:"local_instance_id"`
		GlobalInstanceID       string   `json:"global_instance_id"`
		AdministratorKey       string   `json:"administrator_key"`
		ScopeDigest            string   `json:"scope_digest"`
		SafetyPolicyDigest     string   `json:"safety_policy_digest"`
		ImportBoundsDigest     string   `json:"import_bounds_digest"`
	}{
		SchemaVersion:          "agent-dispatch.sync-acknowledgement-config/v1",
		GroupID:                s.GroupID,
		ResourceID:             s.Resource,
		Resource:               resource,
		RemoteName:             s.RemoteName,
		RemoteRepositoryDigest: s.RemoteRepositoryDigest,
		ContentRef:             s.ContentRef,
		MembershipRef:          s.MembershipRef,
		LocalInstanceID:        s.LocalInstanceID,
		GlobalInstanceID:       cfg.Instance.ID,
		AdministratorKey:       s.AdministratorKey,
		ScopeDigest:            SyncScopeDigest(cfg, s.Resource),
		SafetyPolicyDigest:     SyncSafetyPolicyDigest(),
		ImportBoundsDigest:     SyncImportBoundsDigest(s.Bounds),
	}
	return digestJSON("agent-dispatch.sync-acknowledgement-config/v1", projection), true
}

// syncScopeProjection binds the acknowledgement to every route-owned scope and
// protected/immutable pattern governing the synchronized resource. The fixed
// safety flags above bind the rest of the SYN-010 guard set; neither peer input
// nor runtime membership can weaken those fail-closed rules.
func syncScopeProjection(cfg *Config, resourceID string) []map[string]any {
	ids := make([]string, 0)
	for id, route := range cfg.Routes {
		if route.Source.Resource == resourceID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		route := cfg.Routes[id]
		out = append(out, map[string]any{
			"route_id":  id,
			"include":   sortedCopy(route.Source.Include),
			"exclude":   sortedCopy(route.Source.Exclude),
			"protected": sortedCopy(route.Policy.Protected),
			"immutable": sortedCopy(route.Policy.Immutable),
		})
	}
	return out
}

func SyncAcknowledgementCurrent(cfg *Config, currentStateIncarnation string) bool {
	if cfg.Sync == nil || cfg.Sync.ImportAcknowledgement == nil || currentStateIncarnation == "" {
		return false
	}
	revision, ok := SyncRevision(cfg)
	if !ok {
		return false
	}
	ack := cfg.Sync.ImportAcknowledgement
	declaredIncarnation := ""
	for _, node := range cfg.Sync.Nodes {
		if node.InstanceID == cfg.Sync.LocalInstanceID {
			declaredIncarnation = node.StateIncarnationID
			break
		}
	}
	return ack.SchemaVersion == "agent-dispatch.sync-import-acknowledgement/v1" &&
		ack.AcknowledgementID != "" &&
		ack.GroupID == cfg.Sync.GroupID && ack.ResourceID == cfg.Sync.Resource &&
		ack.RemoteName == cfg.Sync.RemoteName && ack.RemoteRepositoryDigest == cfg.Sync.RemoteRepositoryDigest && ack.ContentRef == cfg.Sync.ContentRef &&
		ack.MembershipRef == cfg.Sync.MembershipRef && ack.ScopeDigest == SyncScopeDigest(cfg, cfg.Sync.Resource) &&
		ack.LocalInstanceID == cfg.Sync.LocalInstanceID && ack.StateIncarnationID == currentStateIncarnation && declaredIncarnation == currentStateIncarnation &&
		ack.AdministratorKey == cfg.Sync.AdministratorKey && ack.SafetyPolicyDigest == SyncSafetyPolicyDigest() &&
		ack.ImportBoundsDigest == SyncImportBoundsDigest(cfg.Sync.Bounds) && ack.ConfigRevision == revision
}

func SyncScopeDigest(cfg *Config, resourceID string) string {
	return digestJSON("agent-dispatch.sync-scope/v1", syncScopeProjection(cfg, resourceID))
}

func SyncSafetyPolicyDigest() string {
	return digestJSON("agent-dispatch.sync-safety-policy/v1", syncSafetyPolicy())
}

func SyncImportBoundsDigest(bounds SyncBounds) string {
	return digestJSON("agent-dispatch.sync-import-bounds/v1", bounds)
}

func digestJSON(domain string, v any) string {
	raw, _ := json.Marshal(v)
	var canonical any
	_ = json.Unmarshal(raw, &canonical)
	enc, _ := json.Marshal(canonical)
	return digestBytes(domain, enc)
}

func digestBytes(domain string, value []byte) string {
	framed := make([]byte, 0, len(domain)+len(value)+2)
	framed = append(framed, domain...)
	framed = append(framed, 0)
	framed = append(framed, value...)
	framed = append(framed, '\n')
	sum := sha256.Sum256(framed)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func syncSafetyPolicy() map[string]bool {
	return map[string]bool{
		"fast_forward_only":           true,
		"resource_writer_guard":       true,
		"participating_writer_idle":   true,
		"durable_preapply_journal":    true,
		"reject_git_instability":      true,
		"reject_overlapping_changes":  true,
		"reject_untracked_overwrite":  true,
		"preserve_disjoint_changes":   true,
		"preserve_out_of_scope":       true,
		"preserve_ignored_files":      true,
		"defer_unproven_disjointness": true,
		"preserve_watchman_evidence":  true,
		"exact_import_suppression":    true,
	}
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
		destinations = append(destinations, destinationProjection(cfg, route, dest))
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
		// The cross-group acknowledgement is behavior-affecting
		// serialization policy (CON-014, E15-T1): flipping it permits
		// cross-group concurrency over the governed resource and must
		// pause the acknowledged route.
		"allow_cross_group_concurrency": route.AllowCrossGroupConcurrency,
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
// workspace, EFFECTIVE serialization group, execution hints, and sorted
// selection conditions (§14, CON-014). The projection carries the
// resolved group — explicit field, deprecated alias, and the
// resource-derived default that equals an explicit default-form value
// all hash identically, because the effective identity is the
// behavior — so any serialization edit changes the destination and
// route revisions and pauses production acknowledgement.
func destinationProjection(cfg *Config, route Route, dest Destination) map[string]any {
	out := map[string]any{
		"id":                  dest.ID,
		"target":              dest.Target,
		"profile":             dest.Profile,
		"skills":              sortedCopy(dest.Skills),
		"workstream":          dest.Workstream,
		"workspace":           dest.Workspace,
		"serialization_group": EffectiveSerializationGroup(route.Source.Resource, dest),
		"execution_hints":     dest.ExecutionHints,
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
	// The declared drain policy joins the route revision (v0.1.6 §5);
	// an absent block projects nothing, so a v0.1.5 configuration
	// without drain keeps its exact revision.
	if drain := drainRouteProjection(n); drain != nil {
		shape["drain"] = drain
	}
	return shape
}

// DestinationRevision computes the deterministic revision of one
// destination as the route projection sees it (§14): the same projection
// the route revision digests for this destination, hashed alone, so a
// destination-qualified edit can report its own revision and pause state
// (used by the E11-T3 mutation commands). The dst- content address itself
// is the ONE canonical derivation in the domain layer
// (records.RevisionOfProjection, E12 epic whole-review round 1) — the same
// function the store boundary verifies against.
func DestinationRevision(cfg *Config, route Route, dest Destination) string {
	enc, err := json.Marshal(destinationProjection(cfg, route, dest))
	if err != nil {
		return ""
	}
	return records.RevisionOfProjection(string(enc))
}

// DestinationProjectionJSON renders the exact deterministic projection
// bytes one destination revision digests (E12-T1): the durable
// destination-revision record persists these bytes beside the revision,
// so an inspection of any stored child can recover the canonical behavior
// projection its revision named. Map keys marshal sorted, so the bytes are
// stable across runs and platforms. Since E15-T1 the projection includes
// the destination's EFFECTIVE serialization group, whose resolution
// needs the route's resource — the caller supplies the whole route.
func DestinationProjectionJSON(cfg *Config, route Route, dest Destination) (string, error) {
	enc, err := json.Marshal(destinationProjection(cfg, route, dest))
	if err != nil {
		return "", err
	}
	return string(enc), nil
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

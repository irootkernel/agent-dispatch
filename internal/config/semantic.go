package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// SemanticValidate applies the checks beyond the JSON Schema
// (configuration-spec §12, §9). It returns errors that fail loading and
// warnings that are recorded on the config (spec §3: a state directory
// inside the watched vault warns).
//
// Deferred §12 checks with their owners: capability-report freshness
// against the installed target and required-capability availability
// against a live probe (E4 --probe-targets; the offline report file is
// checked by the default validate since E8-T3) and runtime activation
// acknowledgement in SQLite (E1-T4 persistence, E3 route commands).
// Resource-root overlap, absolute state_dir and resource roots, map-key
// patterns, and the max_hash_file_bytes floor moved into the default
// validation with E8-T5/M-18.
func SemanticValidate(cfg *Config) (errs []error, warnings []string) {
	errs = append(errs, validateReferences(cfg)...)
	errs = append(errs, validateDestinations(cfg)...)
	errs = append(errs, validateHermesTargets(cfg)...)
	errs = append(errs, validateNotifications(cfg)...)
	errs = append(errs, validateRetryBudgets(cfg)...)
	errs = append(errs, validateSecretRefs(cfg)...)
	errs, warnings = validateStateDir(cfg, errs, warnings)
	errs = append(errs, validateResourceOverlap(cfg)...)
	errs = append(errs, validateAbsolutePaths(cfg)...)
	errs = append(errs, validateMapKeys(cfg)...)
	errs = append(errs, validateMaxHashFloor(cfg)...)
	return errs, warnings
}

// MinimumEligibleHermesVersion is the v0.1.5 eligibility floor (ADR-0017,
// HER-011): Hermes below 0.19.1 is rejected, with no fixed maximum. A
// hermes target may declare a higher floor, never a lower one.
const MinimumEligibleHermesVersion = "0.19.1"

// validateHermesTargets enforces the hermes_targets contract (§14): a
// non-empty board, a parseable minimum_version at or above the 0.19.1
// eligibility floor (HER-011), and exactly the capability_probe
// compatibility mode (ADR-0017).
func validateHermesTargets(cfg *Config) []error {
	ids := make([]string, 0, len(cfg.HermesTargets))
	for id := range cfg.HermesTargets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		t := cfg.HermesTargets[id]
		if strings.TrimSpace(t.Board) == "" {
			errs = append(errs, fmt.Errorf("hermes_targets.%s.board must be non-empty", id))
		}
		minimum := t.MinimumVersion
		if minimum == "" {
			minimum = MinimumEligibleHermesVersion
		}
		parsed, err := records.ParseVersionTriple(minimum)
		if err != nil {
			errs = append(errs, fmt.Errorf("hermes_targets.%s.minimum_version: %v", id, err))
			continue
		}
		floor, _ := records.ParseVersionTriple(MinimumEligibleHermesVersion)
		if parsed.Less(floor) {
			errs = append(errs, fmt.Errorf("hermes_targets.%s.minimum_version %s is below the v0.1.5 eligibility floor %s (HER-011)", id, minimum, MinimumEligibleHermesVersion))
		}
		if t.Compatibility != "capability_probe" {
			errs = append(errs, fmt.Errorf("hermes_targets.%s.compatibility must be capability_probe in v0.1.5 (got %q)", id, t.Compatibility))
		}
	}
	return errs
}

// validateDestinations enforces the destinations[] contract (FAN-001,
// FAN-005, FAN-006, FAN-011, FAN-012): one or more destinations with
// unique stable IDs and non-empty workstreams, a unique non-empty skill
// list, the closed condition vocabulary, the `all`-only fan-out mode,
// and target resolution across hermes_targets and webhook targets.
func validateDestinations(cfg *Config) []error {
	var errs []error
	re := destinationIDPattern
	for routeID, route := range cfg.Routes {
		if route.FanoutMode != "all" {
			errs = append(errs, fmt.Errorf("route %q fanout_mode must be \"all\" in v0.1.5 (got %q)", routeID, route.FanoutMode))
		}
		if len(route.Destinations) == 0 {
			errs = append(errs, fmt.Errorf("route %q must declare one or more destinations under destinations[] (FAN-001)", routeID))
			continue
		}
		seen := make(map[string]bool, len(route.Destinations))
		for i, dest := range route.Destinations {
			where := fmt.Sprintf("route %q destinations[%d]", routeID, i)
			if !re.MatchString(dest.ID) {
				errs = append(errs, fmt.Errorf("%s id %q must match ^[a-z][a-z0-9-]{0,63}$", where, dest.ID))
			}
			if seen[dest.ID] {
				errs = append(errs, fmt.Errorf("%s id %q is declared more than once in the route", where, dest.ID))
			}
			seen[dest.ID] = true
			if strings.TrimSpace(dest.Workstream) == "" {
				errs = append(errs, fmt.Errorf("%s workstream must be non-empty", where))
			}
			resolved, resolvedOK := cfg.ResolveTarget(dest.Target)
			isWebhook := resolvedOK && resolved.IsWebhook()
			// Skills are the Hermes execution envelope: a hermes
			// destination requires a unique non-empty list; a webhook
			// destination takes none.
			if isWebhook {
				if dest.Profile != "" || len(dest.Skills) != 0 || dest.Workspace != "" || dest.MutexKey != "" {
					errs = append(errs, fmt.Errorf("%s (%s) targets webhook target %q; profile, skills, workspace, and mutex_key apply only to a hermes destination", where, dest.ID, dest.Target))
				}
			} else {
				if dest.Profile == "" {
					errs = append(errs, fmt.Errorf("%s (%s) profile must be non-empty for a hermes destination (HER-015)", where, dest.ID))
				}
				if len(dest.Skills) == 0 {
					errs = append(errs, fmt.Errorf("%s (%s) skills must be a non-empty list", where, dest.ID))
				}
				dup := make(map[string]bool, len(dest.Skills))
				for _, s := range dest.Skills {
					if s == "" {
						errs = append(errs, fmt.Errorf("%s (%s) skills contains an empty name", where, dest.ID))
					}
					if dup[s] {
						errs = append(errs, fmt.Errorf("%s (%s) skills list %q more than once", where, dest.ID, s))
					}
					dup[s] = true
				}
			}
			if err := validateConditions(where, dest.Conditions); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

var destinationIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// validateConditions enforces the closed selection vocabulary (FAN-004,
// FAN-005): the five condition classes, structural values from the
// domain enums, and non-empty value lists.
func validateConditions(where string, c *Conditions) error {
	if c == nil {
		return nil
	}
	classes := []struct {
		name  string
		keys  *[]string
		parse func(string) error
	}{
		{"path_include", &c.PathInclude, nil},
		{"path_exclude", &c.PathExclude, nil},
		{"operations", &c.Operations, func(s string) error { _, err := records.ParseOperation(s); return err }},
		{"classifications", &c.Classifications, func(s string) error { _, err := records.ParseClassification(s); return err }},
		{"policy_outcomes", &c.PolicyOutcomes, func(s string) error { _, err := records.ParseDisposition(s); return err }},
	}
	for _, class := range classes {
		if *class.keys == nil {
			continue
		}
		if len(*class.keys) == 0 {
			return fmt.Errorf("%s conditions.%s must not be an empty list (omit the class to select unconditionally)", where, class.name)
		}
		seen := make(map[string]bool, len(*class.keys))
		for _, v := range *class.keys {
			if v == "" {
				return fmt.Errorf("%s conditions.%s contains an empty value", where, class.name)
			}
			if seen[v] {
				return fmt.Errorf("%s conditions.%s lists %q more than once", where, class.name, v)
			}
			seen[v] = true
			if class.parse != nil {
				if err := class.parse(v); err != nil {
					return fmt.Errorf("%s conditions.%s: %v", where, class.name, err)
				}
			}
		}
	}
	return nil
}

// notificationEventVocabulary is the closed v0.1.5 event set (NTF-002):
// the names cover every class the defaults must reach. Delivery arrives
// with E13; v0.1.5 validates the declared policy only.
var notificationEventVocabulary = map[string]bool{
	"work_completed":          true,
	"work_failed":             true,
	"work_exhausted":          true,
	"delivery_unknown":        true,
	"quarantined":             true,
	"reconciliation_required": true,
	"integration_drift":       true,
	"watchman_drift":          true,
}

// validateNotifications enforces the declared per-route notification
// policy (NTF-001, §14): a closed event vocabulary, unique sink IDs, and
// the webhook sink's https endpoint with its authentication shape. A
// notifications block without sinks is valid and disabled.
func validateNotifications(cfg *Config) []error {
	var errs []error
	for routeID, route := range cfg.Routes {
		if route.Notifications == nil {
			continue
		}
		n := route.Notifications
		if len(n.Events) == 0 {
			errs = append(errs, fmt.Errorf("route %q notifications.events must declare at least one event (omit the notifications block when disabled)", routeID))
		}
		for _, e := range n.Events {
			if !notificationEventVocabulary[e] {
				errs = append(errs, fmt.Errorf("route %q notifications event %q is outside the closed v0.1.5 vocabulary", routeID, e))
			}
		}
		seen := make(map[string]bool, len(n.Sinks))
		for i, sink := range n.Sinks {
			where := fmt.Sprintf("route %q notifications.sinks[%d]", routeID, i)
			if !destinationIDPattern.MatchString(sink.ID) {
				errs = append(errs, fmt.Errorf("%s id %q must match ^[a-z][a-z0-9-]{0,63}$", where, sink.ID))
			}
			if seen[sink.ID] {
				errs = append(errs, fmt.Errorf("%s id %q is declared more than once", where, sink.ID))
			}
			seen[sink.ID] = true
			switch sink.Type {
			case "webhook":
				if !strings.HasPrefix(sink.Endpoint, "https://") {
					errs = append(errs, fmt.Errorf("%s (%s) endpoint must be an https URL", where, sink.ID))
				}
				if sink.Auth == nil {
					errs = append(errs, fmt.Errorf("%s (%s) webhook sink requires an auth block with a secret_ref", where, sink.ID))
				}
			case "log":
				if sink.Endpoint != "" || sink.Auth != nil {
					errs = append(errs, fmt.Errorf("%s (%s) log sink takes no endpoint or auth", where, sink.ID))
				}
			default:
				errs = append(errs, fmt.Errorf("%s (%s) type must be webhook or log (got %q)", where, sink.ID, sink.Type))
			}
		}
	}
	return errs
}

// validateResourceOverlap rejects resources whose canonicalized roots
// nest inside one another (§12, E8-T5/M-18): two resources watching the
// same tree double-report every change and collide on path facts.
func validateResourceOverlap(cfg *Config) []error {
	type watched struct{ id, clean, resolved string }
	ids := make([]string, 0, len(cfg.Resources))
	for id := range cfg.Resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	list := make([]watched, 0, len(ids))
	for _, id := range ids {
		r := cfg.Resources[id]
		clean := filepath.Clean(r.Root)
		resolved := clean
		if out, err := filepath.EvalSymlinks(clean); err == nil {
			resolved = out
		}
		list = append(list, watched{id, clean, resolved})
	}
	nests := func(a, b string) bool {
		return a == b || strings.HasPrefix(a+string(filepath.Separator), b+string(filepath.Separator)) ||
			strings.HasPrefix(b+string(filepath.Separator), a+string(filepath.Separator))
	}
	var errs []error
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			// Compare within each form: mixing a resolved root with an
			// unresolved one (a resolution failure on exactly one side,
			// as with a not-yet-existing candidate root behind a symlink
			// prefix) hides the nesting.
			if nests(list[i].clean, list[j].clean) || nests(list[i].resolved, list[j].resolved) {
				errs = append(errs, fmt.Errorf("resources %q (%s) and %q (%s) watch overlapping roots", list[i].id, list[i].clean, list[j].id, list[j].clean))
			}
		}
	}
	return errs
}

// validateAbsolutePaths requires the state directory and every resource
// root to be absolute (§12, E8-T5/M-18): a relative value resolves
// against whatever directory the operator happens to run from, so the
// database location would depend on the caller's cwd.
func validateAbsolutePaths(cfg *Config) []error {
	var errs []error
	if cfg.Instance.StateDir != "" && !filepath.IsAbs(cfg.Instance.StateDir) {
		errs = append(errs, fmt.Errorf("instance.state_dir %q must be absolute", cfg.Instance.StateDir))
	}
	ids := make([]string, 0, len(cfg.Resources))
	for id := range cfg.Resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if root := cfg.Resources[id].Root; !filepath.IsAbs(root) {
			errs = append(errs, fmt.Errorf("resources.%s.root %q must be absolute", id, root))
		}
	}
	return errs
}

// validateMapKeys constrains the resources/targets/routes map keys to
// the same identifier grammar the schema applies to scalar names
// (§12, E8-T5/M-18).
func validateMapKeys(cfg *Config) []error {
	re := regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	var errs []error
	add := func(kind, id string) {
		if !re.MatchString(id) {
			errs = append(errs, fmt.Errorf("%s key %q must match ^[a-z][a-z0-9-]{0,63}$", kind, id))
		}
	}
	for id := range cfg.Resources {
		add("resources", id)
	}
	for id := range cfg.Targets {
		add("targets", id)
	}
	for id := range cfg.HermesTargets {
		add("hermes_targets", id)
	}
	for id := range cfg.Routes {
		add("routes", id)
	}
	return errs
}

// validateMaxHashFloor rejects a zero max_hash_file_bytes (§2 limits,
// E8-T5/M-18): validate accepted it while reconcile refused it at run
// time, so the same document passed one surface and failed another.
func validateMaxHashFloor(cfg *Config) []error {
	if v := cfg.Limits.MaxHashFileBytes; v != nil && *v <= 0 {
		return []error{fmt.Errorf("limits.max_hash_file_bytes must be positive when set (got %d)", *v)}
	}
	return nil
}

// validateReferences fails closed when a route names an unknown resource
// or a destination names an unknown target (§12, §14), and when one ID
// is declared in both target maps: a hermes target and a webhook target
// sharing an ID would make every destination binding on it ambiguous.
func validateReferences(cfg *Config) []error {
	var errs []error
	for id := range cfg.Targets {
		if _, clash := cfg.HermesTargets[id]; clash {
			errs = append(errs, fmt.Errorf("target %q is declared under both targets and hermes_targets; destination bindings on a shared ID are ambiguous", id))
		}
	}
	for routeID, route := range cfg.Routes {
		if _, ok := cfg.Resources[route.Source.Resource]; !ok {
			errs = append(errs, fmt.Errorf("route %q references unknown resource %q", routeID, route.Source.Resource))
		}
		for i, dest := range route.Destinations {
			if _, ok := cfg.ResolveTarget(dest.Target); !ok {
				errs = append(errs, fmt.Errorf("route %q destinations[%d] (%s) references unknown target %q (declare it under hermes_targets or targets)", routeID, i, dest.ID, dest.Target))
			}
		}
	}
	return errs
}

// validateRetryBudgets enforces the §9 semantic bounds the schema cannot
// express: max backoff at least the initial backoff.
func validateRetryBudgets(cfg *Config) []error {
	var errs []error
	for routeID, route := range cfg.Routes {
		r := route.SubmissionRetry
		initial, err := parseDuration(r.InitialBackoff)
		if err != nil {
			errs = append(errs, fmt.Errorf("route %q: initial_backoff: %v", routeID, err))
			continue
		}
		max, err := parseDuration(r.MaxBackoff)
		if err != nil {
			errs = append(errs, fmt.Errorf("route %q: max_backoff: %v", routeID, err))
			continue
		}
		if max.Cmp(initial) < 0 {
			errs = append(errs, fmt.Errorf("route %q: max_backoff %s is below initial_backoff %s", routeID, r.MaxBackoff, r.InitialBackoff))
		}
	}
	return errs
}

// validateStateDir applies the §3 policy: a state directory inside a
// governed resource root produces a warning (spec: "Validation warns if it
// is"); an unusable path is an error.
func validateStateDir(cfg *Config, errs []error, warnings []string) ([]error, []string) {
	dir := cfg.Instance.StateDir
	if dir == "" {
		return errs, warnings
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return append(errs, fmt.Errorf("instance.state_dir: %w", err)), warnings
	}
	for resourceID, res := range cfg.Resources {
		root, err := filepath.Abs(res.Root)
		if err != nil {
			continue
		}
		if withinDir(abs, root) {
			warnings = append(warnings, fmt.Sprintf("instance.state_dir %q is inside resource %q root %q; state must live outside the watched vault", dir, resourceID, res.Root))
		}
	}
	return errs, warnings
}

func withinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// validateSecretRefs parses every secret reference without resolving it
// (SEC-006): a malformed reference fails validation. Webhook target and
// notification-sink authentication additionally requires its declared
// shape — header authentication names the header, bearer authentication
// carries no header name (configuration-spec §5, §12).
func validateSecretRefs(cfg *Config) []error {
	var errs []error
	for targetID, target := range cfg.Targets {
		if target.Auth == nil {
			continue
		}
		if _, err := ParseSecretRef(target.Auth.SecretRef); err != nil {
			errs = append(errs, fmt.Errorf("target %q auth.secret_ref: %v", targetID, err))
		}
		if target.Type == "hermes-webhook" {
			errs = append(errs, validateAuthShape(fmt.Sprintf("target %q", targetID), target.Auth)...)
		}
	}
	for routeID, route := range cfg.Routes {
		if route.Notifications == nil {
			continue
		}
		for i, sink := range route.Notifications.Sinks {
			if sink.Auth == nil {
				continue
			}
			where := fmt.Sprintf("route %q notifications.sinks[%d] (%s)", routeID, i, sink.ID)
			if _, err := ParseSecretRef(sink.Auth.SecretRef); err != nil {
				errs = append(errs, fmt.Errorf("%s auth.secret_ref: %v", where, err))
			}
			if sink.Type == "webhook" {
				errs = append(errs, validateAuthShape(where, sink.Auth)...)
			}
		}
	}
	return errs
}

// validateAuthShape enforces the bearer/header shape rules on one
// authentication block.
func validateAuthShape(where string, auth *Auth) []error {
	var errs []error
	switch auth.Type {
	case "header":
		if auth.HeaderName == "" {
			errs = append(errs, fmt.Errorf("%s auth.header_name: required when auth.type is header", where))
		}
	case "bearer":
		if auth.HeaderName != "" {
			errs = append(errs, fmt.Errorf("%s auth.header_name: must be empty when auth.type is bearer", where))
		}
	}
	return errs
}

// durationPattern is the schema duration form: a positive integer with a
// unit suffix (no zero, no fractions).
var durationPattern = regexp.MustCompile(`^([1-9][0-9]*)(ms|s|m|h|d)$`)

// Duration is a parsed configuration duration in nanoseconds.
type Duration struct {
	Nanos int64
	Unit  string
	Text  string
}

// Cmp returns -1, 0, or 1 comparing d against o.
func (d Duration) Cmp(o Duration) int {
	switch {
	case d.Nanos < o.Nanos:
		return -1
	case d.Nanos > o.Nanos:
		return 1
	}
	return 0
}

func (d Duration) String() string { return d.Text }

var unitNanos = map[string]int64{
	"ms": int64(1e6),
	"s":  int64(1e9),
	"m":  60 * int64(1e9),
	"h":  3600 * int64(1e9),
	"d":  24 * 3600 * int64(1e9),
}

// parseDuration parses the schema duration form without loss (kept as an
// exact integer of nanoseconds).
func parseDuration(text string) (Duration, error) {
	return ParseDuration(text)
}

// ParseDuration is the exported schema-exact duration parser; every
// layer that must accept exactly the configuration schema's duration
// syntax (positive integer with ms, s, m, h, or d unit) uses this one
// parser instead of re-implementing the grammar.
func ParseDuration(text string) (Duration, error) {
	m := durationPattern.FindStringSubmatch(text)
	if m == nil {
		return Duration{}, fmt.Errorf("invalid duration %q (want positive integer with ms, s, m, h, or d unit)", text)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return Duration{}, fmt.Errorf("duration %q overflows: %v", text, err)
	}
	unit := unitNanos[m[2]]
	if n > (1<<63-1)/unit {
		return Duration{}, fmt.Errorf("duration %q overflows int64 nanoseconds", text)
	}
	return Duration{Nanos: n * unit, Unit: m[2], Text: text}, nil
}

// StateDirInsideRootWarning returns the section 3 warning text when dir is
// inside the given resource root, or "" when it is not.
func StateDirInsideRootWarning(dir, root string) string {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	if withinDir(absDir, absRoot) {
		return fmt.Sprintf("state directory %s is inside the watched resource root %s; state must live outside the vault", dir, root)
	}
	return ""
}

package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	errs = append(errs, validateRetryBudgets(cfg)...)
	errs = append(errs, validateSecretRefs(cfg)...)
	errs, warnings = validateStateDir(cfg, errs, warnings)
	errs = append(errs, validateResourceOverlap(cfg)...)
	errs = append(errs, validateAbsolutePaths(cfg)...)
	errs = append(errs, validateMapKeys(cfg)...)
	errs = append(errs, validateMaxHashFloor(cfg)...)
	return errs, warnings
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
// or target (§12).
func validateReferences(cfg *Config) []error {
	var errs []error
	for routeID, route := range cfg.Routes {
		if _, ok := cfg.Resources[route.Source.Resource]; !ok {
			errs = append(errs, fmt.Errorf("route %q references unknown resource %q", routeID, route.Source.Resource))
		}
		if _, ok := cfg.Targets[route.Dispatch.Target]; !ok {
			errs = append(errs, fmt.Errorf("route %q references unknown target %q", routeID, route.Dispatch.Target))
		}
	}
	return errs
}

// validateRetryBudgets enforces the §9 semantic bounds the schema cannot
// express: max backoff at least the initial backoff.
func validateRetryBudgets(cfg *Config) []error {
	var errs []error
	for routeID, route := range cfg.Routes {
		r := route.Dispatch.SubmissionRetry
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
// (SEC-006): a malformed reference fails validation. Webhook
// authentication additionally requires its declared shape — header
// authentication names the header, bearer authentication carries no
// header name (configuration-spec §5, §12).
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
			switch target.Auth.Type {
			case "header":
				if target.Auth.HeaderName == "" {
					errs = append(errs, fmt.Errorf("target %q auth.header_name: required when auth.type is header", targetID))
				}
			case "bearer":
				if target.Auth.HeaderName != "" {
					errs = append(errs, fmt.Errorf("target %q auth.header_name: must be empty when auth.type is bearer", targetID))
				}
			}
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

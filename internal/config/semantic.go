package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SemanticValidate applies the checks beyond the JSON Schema
// (configuration-spec §12, §9). It returns errors that fail loading and
// warnings that are recorded on the config (spec §3: a state directory
// inside the watched vault warns).
//
// Deferred §12 checks with their owners: resource-root overlap resolution
// and symlink canonicalization (E2-T2 safe-path engine), capability-report
// freshness against the installed target and required-capability
// availability (E4, verified by config validate --probe-targets), and
// runtime activation acknowledgement in SQLite (E1-T4 persistence, E3
// route commands).
func SemanticValidate(cfg *Config) (errs []error, warnings []string) {
	errs = append(errs, validateReferences(cfg)...)
	errs = append(errs, validateRetryBudgets(cfg)...)
	errs = append(errs, validateSecretRefs(cfg)...)
	errs, warnings = validateStateDir(cfg, errs, warnings)
	return errs, warnings
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
// (SEC-006): a malformed reference fails validation.
func validateSecretRefs(cfg *Config) []error {
	var errs []error
	for targetID, target := range cfg.Targets {
		if target.Auth == nil {
			continue
		}
		if _, err := ParseSecretRef(target.Auth.SecretRef); err != nil {
			errs = append(errs, fmt.Errorf("target %q auth.secret_ref: %v", targetID, err))
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

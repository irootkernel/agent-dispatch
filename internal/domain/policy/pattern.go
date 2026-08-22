// Package policy implements the deterministic path pattern engine (E2-T2,
// PTH-003, PTH-007, PTH-008, configuration-spec §7): operator-owned
// include/exclude/protected/immutable globs over normalized slash-separated
// relative paths, with `**` recursive matching (`*` and `?` are
// byte-oriented single-segment wildcards) and identical behavior on macOS
// and Linux for the same normalized path and case mode. Matching is
// linear-time dynamic programming, so hostile long paths cannot trigger
// exponential backtracking. Patterns and
// paths are data: a path can only be classified, never change the patterns
// or any route authority (PTH-004, SEC-003).
package policy

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// CaseMode selects pattern case behavior (configuration-spec §7). v0.1
// default is `filesystem`, resolved by the caller from the host; the
// engine itself stays deterministic for a fixed mode.
type CaseMode string

const (
	CaseFilesystem  CaseMode = "filesystem"
	CaseSensitive   CaseMode = "sensitive"
	CaseInsensitive CaseMode = "insensitive"
)

// Status classifies one normalized relative path against the engine's
// operator-owned pattern sets.
type Status string

const (
	// StatusExcluded means the path is out of scope: not matched by the
	// include set, or matched by the exclude set or a default exclusion.
	// Exclusions take precedence over include; protected and immutable
	// are evaluated afterwards (configuration-spec §7).
	StatusExcluded Status = "excluded"
	// StatusProtected marks operator-protected paths (PTH-008): the
	// caller must quarantine, never include in an automatic task.
	StatusProtected Status = "protected"
	// StatusImmutable marks operator-immutable paths.
	StatusImmutable Status = "immutable"
	// StatusNormal is in scope with no special handling.
	StatusNormal Status = "normal"
)

// DefaultExclusions are the built-in structural exclusions every route
// inherits in addition to its configured exclude list (PTH-007): Watchman
// bookkeeping (cookie files and state, matched at any depth), `.git/**`,
// and OS metadata-only files. Obsidian UI state files (for example
// `.obsidian/workspace*.json`) are operator-configured excludes, per the
// configuration-spec example.
var DefaultExclusions = []string{
	".git/**",
	"**/.git/**",
	".watchman-cookie-*",
	"**/.watchman-cookie-*",
	".watchman-state*",
	"**/.watchman-state*",
	".DS_Store",
	"**/.DS_Store",
}

// segment is one compiled path segment of a pattern. Only `**` is
// structural; every other segment carries its glob text.
type segment struct {
	text      string
	matchText string // folded for matching; equals text when case-sensitive
	recursive bool   // "**"
}

// DefaultMaxPathBytes caps the event-path length accepted by Classify
// (SEC-009) at the same envelope as the filesystem resolver, so a path of
// pathological depth cannot burn CPU in matching even before any
// filesystem access.
const DefaultMaxPathBytes = 4096

// Engine is an immutable compiled pattern set. Build once per route
// snapshot and reuse; it holds no per-call state.
type Engine struct {
	include      [][]segment
	exclude      [][]segment
	protected    [][]segment
	immutable    [][]segment
	fold         bool
	maxPathBytes int
}

// NewEngine compiles the operator-owned pattern sets. Every pattern must
// be a valid relative glob: non-empty, no backslashes, no NUL, no "." or
// ".." segments, no leading "/", and `**` only as a whole segment.
// CaseFilesystem is host-dependent and MUST be resolved by the caller to
// CaseSensitive or CaseInsensitive before compiling, so engine behavior
// is deterministic and recorded in the route revision.
func NewEngine(include, exclude, protected, immutable []string, mode CaseMode) (*Engine, error) {
	switch mode {
	case CaseSensitive, CaseInsensitive:
	default:
		return nil, fmt.Errorf("case mode %q must be resolved to sensitive or insensitive before compiling", mode)
	}
	e := &Engine{fold: mode == CaseInsensitive, maxPathBytes: DefaultMaxPathBytes}
	var err error
	if e.include, err = compileAll(include, "include"); err != nil {
		return nil, err
	}
	if e.exclude, err = compileAll(exclude, "exclude"); err != nil {
		return nil, err
	}
	if e.protected, err = compileAll(protected, "protected"); err != nil {
		return nil, err
	}
	if e.immutable, err = compileAll(immutable, "immutable"); err != nil {
		return nil, err
	}
	// Default exclusions are structural and cannot be disabled (PTH-007).
	defaults, err := compileAll(DefaultExclusions, "default exclusion")
	if err != nil {
		return nil, err
	}
	e.exclude = append(e.exclude, defaults...)
	if e.fold {
		// Fold only the matching text; `text` keeps the configured form
		// so Patterns() reports what the operator wrote.
		for _, set := range [][][]segment{e.include, e.exclude, e.protected, e.immutable} {
			for _, pat := range set {
				for i := range pat {
					pat[i].matchText = strings.ToLower(pat[i].matchText)
				}
			}
		}
	}
	return e, nil
}

func compileAll(patterns []string, what string) ([][]segment, error) {
	compiled := make([][]segment, 0, len(patterns))
	for _, p := range patterns {
		segs, err := compile(p)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %v", what, p, err)
		}
		compiled = append(compiled, segs)
	}
	return compiled, nil
}

func compile(pattern string) ([]segment, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}
	if !utf8.ValidString(pattern) {
		return nil, fmt.Errorf("not valid UTF-8")
	}
	if strings.ContainsRune(pattern, 0) {
		return nil, fmt.Errorf("contains NUL")
	}
	if strings.HasPrefix(pattern, "/") {
		return nil, fmt.Errorf("must be relative")
	}
	if strings.ContainsRune(pattern, '\\') {
		return nil, fmt.Errorf("must use / separators")
	}
	if strings.HasPrefix(pattern, "./") || strings.HasSuffix(pattern, "/.") {
		return nil, fmt.Errorf("cannot contain . segments")
	}
	var segs []segment
	for _, raw := range strings.Split(pattern, "/") {
		if raw == "" {
			return nil, fmt.Errorf("empty segment")
		}
		if raw == ".." {
			return nil, fmt.Errorf("cannot contain .. segments")
		}
		if raw == "." {
			return nil, fmt.Errorf("cannot contain . segments")
		}
		if raw == "**" {
			segs = append(segs, segment{text: "**", matchText: "**", recursive: true})
			continue
		}
		if strings.Contains(raw, "**") {
			return nil, fmt.Errorf("** must be a whole segment")
		}
		segs = append(segs, segment{text: raw, matchText: raw})
	}
	return segs, nil
}

// SetMaxPathBytes applies an explicit path-length cap; zero or negative
// restores the default.
func (e *Engine) SetMaxPathBytes(max int) {
	if max > 0 {
		e.maxPathBytes = max
	} else {
		e.maxPathBytes = DefaultMaxPathBytes
	}
}

// Classify classifies one normalized relative path. The path is data
// only: it selects a status and can never alter the compiled patterns.
// An error means the path is unusable as input (encoding, traversal,
// length): the returned status is the empty zero value so a caller
// ignoring the error cannot mistake it for a real classification, and
// callers must treat it as an input defect, never as an exclusion.
func (e *Engine) Classify(path string) (Status, error) {
	if len(path) > e.maxPathBytes {
		return "", fmt.Errorf("path exceeds %d bytes", e.maxPathBytes)
	}
	if _, err := records.NormalizePath(path); err != nil {
		return "", fmt.Errorf("unusable path: %v", err)
	}
	names := strings.Split(path, "/")
	if e.fold {
		names = foldAll(names)
	}
	if matchAny(e.exclude, names) || !matchAny(e.include, names) {
		return StatusExcluded, nil
	}
	if matchAny(e.protected, names) {
		return StatusProtected, nil
	}
	if matchAny(e.immutable, names) {
		return StatusImmutable, nil
	}
	return StatusNormal, nil
}

// ClassifyMany classifies many paths in one call; the result order
// matches the input order.
func (e *Engine) ClassifyMany(paths []string) ([]Status, error) {
	out := make([]Status, len(paths))
	for i, p := range paths {
		st, err := e.Classify(p)
		if err != nil {
			return nil, fmt.Errorf("path %d: %w", i, err)
		}
		out[i] = st
	}
	return out, nil
}

// Patterns returns the engine's effective pattern sets including the
// appended defaults, deterministically sorted, for revision and
// diagnostics. The include set is returned as configured.
func (e *Engine) Patterns() (include, exclude, protected, immutable []string) {
	dump := func(set [][]segment) []string {
		out := make([]string, 0, len(set))
		for _, segs := range set {
			parts := make([]string, len(segs))
			for i, s := range segs {
				parts[i] = s.text
			}
			out = append(out, strings.Join(parts, "/"))
		}
		sort.Strings(out)
		return out
	}
	return dump(e.include), dump(e.exclude), dump(e.protected), dump(e.immutable)
}

func matchAny(set [][]segment, names []string) bool {
	for _, pat := range set {
		if matchSegments(pat, names) {
			return true
		}
	}
	return false
}

// matchSegments matches compiled pattern segments against path segments in
// O(len(pat)*len(names)) dynamic programming, so any number of `**`
// segments cannot cause exponential backtracking. `**` matches zero or
// more whole segments; `*` and `?` stay inside one segment.
func matchSegments(pat []segment, names []string) bool {
	// dp[j] is true when the pattern consumed so far matches names[:j].
	dp := make([]bool, len(names)+1)
	dp[0] = true
	for i := 0; i < len(pat); i++ {
		if pat[i].recursive {
			// `**` consumes zero or more names: dp[j] stays true once
			// any earlier prefix matched.
			any := false
			for j := 0; j <= len(names); j++ {
				any = any || dp[j]
				dp[j] = any
			}
			continue
		}
		// Ordinary segment: consume exactly one name, iterating
		// downward so this round only reads the previous round's values.
		for j := len(names); j >= 1; j-- {
			dp[j] = dp[j-1] && matchSegment(pat[i].matchText, names[j-1])
		}
		dp[0] = false
	}
	return dp[len(names)]
}

// matchSegment implements single-segment glob matching with `*` (any run
// of bytes) and `?` (exactly one byte). Matching is byte-oriented, so `?`
// does not match one multi-byte character; the operator vocabulary is
// deliberately tiny, portable, and ASCII-oriented. No character classes.
func matchSegment(pattern, name string) bool {
	// Iterative two-pointer backtracking.
	var pi, ni, starP, starN = 0, 0, -1, 0
	for ni < len(name) {
		switch {
		case pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == name[ni]):
			pi++
			ni++
		case pi < len(pattern) && pattern[pi] == '*':
			starP = pi
			starN = ni
			pi++
		case starP >= 0:
			starN++
			ni = starN
			pi = starP + 1
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

func foldAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.ToLower(n)
	}
	return out
}

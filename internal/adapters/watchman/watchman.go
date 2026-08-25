// Package watchman parses real Watchman trigger input into the bounded
// source-input DTO for ingestion (E2-T1). It follows the frozen E0-T5
// evidence (docs/integrations/watchman-public-interface-report.md and the
// corpus under docs/integrations/fixtures/watchman/): the stdin payload is
// a bare JSON array of file objects with no route, resource, or target
// information of any kind (SRC-003), and the only trusted context is the
// allowlisted WATCHMAN_* environment (SRC-002).
package watchman

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// Env value bound; longer values are rejected before any parsing
// (SEC-009 env-size limit). Watchman paths and clock tokens stay far
// below this.
const MaxEnvValueBytes = 4096

// Env is the trusted trigger environment parsed from the allowlisted
// WATCHMAN_* variables (SRC-002). Every other environment variable is
// ignored: only these names are ever read.
type Env struct {
	Trigger      string
	Root         string
	RelativeRoot string // empty when absent
	HasRelative  bool
	Since        string // previous position; empty on the first invocation after definition, replacement, or re-watch
	Clock        string // current position; always set
	Sock         string
}

// Position is the opaque source position pair. Clock and since tokens are
// never parsed or validated locally (E0-T5 §6: clocks are opaque).
type Position struct {
	Since string // empty when absent
	Clock string
}

// HasSince reports whether a trustworthy previous position exists. A
// missing position gets the same conservative handling as overflow
// (architecture watchman-integration §7).
func (p Position) HasSince() bool { return p.Since != "" }

// ParseEnv extracts the allowlisted environment (SRC-002). getenv must
// return the value and presence of one variable; pass os.LookupEnv at the
// edge. WATCHMAN_TRIGGER, WATCHMAN_ROOT, and WATCHMAN_CLOCK are required;
// WATCHMAN_SINCE and WATCHMAN_RELATIVE_ROOT are optional; WATCHMAN_FILES_OVERFLOW
// is refuted by evidence (0/23 invocations) and is deliberately absent from
// the allowlist, so a payload cannot smuggle an overflow flag through the
// environment. SEC-009: WATCHMAN_SOCK is captured for diagnostics but is
// not part of the source model.
func ParseEnv(getenv func(string) (string, bool)) (Env, error) {
	lookup := func(name string) (string, error) {
		v, ok := getenv(name)
		if !ok {
			return "", fmt.Errorf("%s is not set", name)
		}
		if v == "" {
			return "", fmt.Errorf("%s is empty", name)
		}
		if len(v) > MaxEnvValueBytes {
			return "", fmt.Errorf("%s exceeds %d bytes", name, MaxEnvValueBytes)
		}
		return v, nil
	}
	trigger, err := lookup("WATCHMAN_TRIGGER")
	if err != nil {
		return Env{}, err
	}
	root, err := lookup("WATCHMAN_ROOT")
	if err != nil {
		return Env{}, err
	}
	clock, err := lookup("WATCHMAN_CLOCK")
	if err != nil {
		return Env{}, err
	}
	env := Env{Trigger: trigger, Root: root, Clock: clock}
	if since, ok := getenv("WATCHMAN_SINCE"); ok {
		if since == "" {
			return Env{}, fmt.Errorf("WATCHMAN_SINCE is empty")
		}
		if len(since) > MaxEnvValueBytes {
			return Env{}, fmt.Errorf("WATCHMAN_SINCE exceeds %d bytes", MaxEnvValueBytes)
		}
		env.Since = since
	}
	if rel, ok := getenv("WATCHMAN_RELATIVE_ROOT"); ok {
		if rel == "" {
			return Env{}, fmt.Errorf("WATCHMAN_RELATIVE_ROOT is empty")
		}
		if len(rel) > MaxEnvValueBytes {
			return Env{}, fmt.Errorf("WATCHMAN_RELATIVE_ROOT exceeds %d bytes", MaxEnvValueBytes)
		}
		env.RelativeRoot = rel
		env.HasRelative = true
	}
	if sock, ok := getenv("WATCHMAN_SOCK"); ok {
		if sock == "" {
			return Env{}, fmt.Errorf("WATCHMAN_SOCK is empty")
		}
		if len(sock) > MaxEnvValueBytes {
			return Env{}, fmt.Errorf("WATCHMAN_SOCK exceeds %d bytes", MaxEnvValueBytes)
		}
		env.Sock = sock
	}
	return env, nil
}

// Flags derives the observation flags. Overflow is never true at parse
// time: WATCHMAN_FILES_OVERFLOW was refuted (E0-T5 §3) and the
// overflow-class signal is the missing position itself, which is carried
// as FreshInstance per architecture watchman-integration §7.
func (e Env) Flags() records.SourceFlags {
	return records.SourceFlags{
		FreshInstance: e.Since == "",
		RelativeRoot:  e.RelativeRoot,
		HasRelative:   e.HasRelative,
	}
}

// SourceEventKey derives the retransmission key from stable trigger
// identity plus position (architecture watchman-integration §8):
//
//	watchman:<source-id>:<since>:<clock>:<payload-digest>
//
// When no trustworthy position exists the key is empty, encoding null, and
// source-level deduplication must not be claimed.
func SourceEventKey(sourceID string, pos Position, digest records.Digest) string {
	if !pos.HasSince() {
		return ""
	}
	return fmt.Sprintf("watchman:%s:%s:%s:%s", sourceID, pos.Since, pos.Clock, digest)
}

// Binding is the persisted managed Watchman binding (E10-T2, SRC-009):
// the four distinct values every lifecycle command resolves and reports
// identically — the configured resource root, the actual watch root
// Watchman canonicalized (which may be an ancestor of the configured
// root), the configured-root-relative path between them, and the stable
// trigger name.
type Binding struct {
	RouteID        string
	ResourceID     string
	ConfiguredRoot string
	ActualRoot     string
	RelativeRoot   string // "." when the actual root is the configured root
	TriggerName    string
	UpdatedAt      string
}

// ValidateBinding checks that the trusted environment matches the trusted
// route binding (SRC-003): the invoked trigger must be the route's trigger
// and the watch root must resolve to the configured resource root. The
// payload plays no part in binding, so it can never select a route or
// resource. Roots compare after cleaning separators, and — when either
// side exists on disk — after symlink resolution, so a configured root
// expressed through a symlink (macOS /tmp vs /private/tmp) still binds
// against Watchman's canonical WATCHMAN_ROOT.
//
// An ancestor actual root binds only through the persisted managed
// binding (E10-T2, SRC-011). The frozen interface evidence
// (trigger-invocation-environment) records that Watchman sets
// WATCHMAN_RELATIVE_ROOT to the ABSOLUTE subdirectory path while
// WATCHMAN_ROOT remains the watch root, so the absolute form is the
// accepted truth; the relative form is accepted only when it is exactly
// the persisted relative root. Either way the recorded actual root must
// match the environment watch root and the join of the two must resolve
// to the configured resource root, so a forged or drifted pair fails
// closed, and plan/dry-run surfaces with no stored binding validate the
// exact root only.
func ValidateBinding(env Env, triggerName, resourceRoot string, stored *Binding) error {
	if env.Trigger != triggerName {
		return fmt.Errorf("trigger binding mismatch: environment trigger %q is not the configured trigger %q", env.Trigger, triggerName)
	}
	if !env.HasRelative {
		if rootsEquivalent(env.Root, resourceRoot) {
			return nil
		}
		return fmt.Errorf("root binding mismatch: environment root %q is not the configured resource root %q (symlinked roots must resolve to the same directory)", env.Root, resourceRoot)
	}
	if stored == nil {
		return fmt.Errorf("root binding mismatch: environment reports relative root %q under watch root %q, but no managed Watchman binding is persisted for this route; run watchman install", env.RelativeRoot, env.Root)
	}
	if env.Trigger != stored.TriggerName {
		return fmt.Errorf("trigger binding mismatch: environment trigger %q is not the bound trigger %q", env.Trigger, stored.TriggerName)
	}
	if CanonicalRoot(env.Root) != CanonicalRoot(stored.ActualRoot) {
		return fmt.Errorf("root binding mismatch: environment root %q is not the bound actual watch root %q (the binding drifted; rerun watchman install)", env.Root, stored.ActualRoot)
	}
	if filepath.IsAbs(env.RelativeRoot) {
		// The frozen-evidence absolute form: the environment names the
		// configured root itself, and it must be exactly where the
		// stored relative root lands under the stored actual root.
		if !rootsEquivalent(env.RelativeRoot, resourceRoot) {
			return fmt.Errorf("root binding mismatch: environment relative root %q does not resolve to the configured resource root %q", env.RelativeRoot, resourceRoot)
		}
		joined := filepath.Join(CanonicalRoot(stored.ActualRoot), filepath.FromSlash(stored.RelativeRoot))
		if !rootsEquivalent(joined, resourceRoot) {
			return fmt.Errorf("root binding mismatch: the bound actual root %q joined with the bound relative root %q does not resolve to the configured resource root %q", stored.ActualRoot, stored.RelativeRoot, resourceRoot)
		}
		return nil
	}
	if ToSlashClean(env.RelativeRoot) != ToSlashClean(stored.RelativeRoot) {
		return fmt.Errorf("root binding mismatch: environment relative root %q is not the bound relative root %q", env.RelativeRoot, stored.RelativeRoot)
	}
	joined := filepath.Join(env.Root, filepath.FromSlash(env.RelativeRoot))
	if !rootsEquivalent(joined, resourceRoot) {
		return fmt.Errorf("root binding mismatch: watch root %q joined with relative root %q does not resolve to the configured resource root %q", env.Root, env.RelativeRoot, resourceRoot)
	}
	return nil
}

// RelativeRootBetween returns the configured-root-relative path from the
// actual watch root to the configured resource root (SRC-011's pure
// computation, shared by every lifecycle command): "." when the two
// resolve to the same directory, or a failure when the configured root is
// not inside the actual root. Both sides are cleaned and, when they
// exist, symlink-resolved before the relationship is decided.
func RelativeRootBetween(actualRoot, configuredRoot string) (string, error) {
	ca, cb := canonicalRoot(actualRoot), canonicalRoot(configuredRoot)
	if ca == cb {
		return ".", nil
	}
	rel, err := filepath.Rel(ca, cb)
	if err != nil {
		return "", fmt.Errorf("configured root %q cannot be expressed relative to the watch root %q", configuredRoot, actualRoot)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("configured root %q is outside the actual watch root %q", configuredRoot, actualRoot)
	}
	return rel, nil
}

// CanonicalRoot cleans a root and resolves symlinks when the path exists
// (the exported form the lifecycle surfaces use for drift comparison).
func CanonicalRoot(root string) string {
	return canonicalRoot(root)
}

// ToSlashClean normalizes a relative root for comparison: cleaned, forward
// slashes, no trailing separator.
func ToSlashClean(rel string) string {
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned == "/" {
		return "."
	}
	return cleaned
}

// canonicalRoot cleans a root and resolves symlinks when the path exists.
func canonicalRoot(root string) string {
	c := filepath.Clean(root)
	if resolved, err := filepath.EvalSymlinks(c); err == nil {
		return resolved
	}
	return c
}

// rootsEquivalent compares two absolute roots after cleaning and, when
// resolvable, symlink canonicalization.
func rootsEquivalent(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if ca == cb {
		return true
	}
	if ra, err := filepath.EvalSymlinks(ca); err == nil {
		ca = ra
	}
	if rb, err := filepath.EvalSymlinks(cb); err == nil {
		cb = rb
	}
	return ca == cb
}

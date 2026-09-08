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
// Watchman canonicalized, the configured-root-relative path between
// them, and the stable trigger name. D-028: the actual root is the
// configured resource root itself and RelativeRoot is the
// schema-vestigial constant "." (the watch_bindings column stays NOT
// NULL, so the value is written rather than dropped).
type Binding struct {
	RouteID        string
	ResourceID     string
	ConfiguredRoot string
	ActualRoot     string
	RelativeRoot   string // "." always (D-028); retained only for the schema column
	TriggerName    string
	UpdatedAt      string
}

// ValidateBinding checks that the trusted environment matches the
// trusted route binding (SRC-003, SRC-013): the invoked trigger must be
// the route's trigger and the canonicalized WATCHMAN_ROOT must be the
// configured resource root itself. The payload plays no part in
// binding, so it can never select a route or resource. Roots compare
// after cleaning separators and — when either side exists on disk —
// after symlink resolution, so a configured root expressed through a
// symlink (macOS /tmp vs /private/tmp) still binds against Watchman's
// canonical WATCHMAN_ROOT.
//
// D-028: there is no ancestor binding and no second axis. The
// configuration is the trust anchor, so no persisted record is
// consulted, and a present WATCHMAN_RELATIVE_ROOT is the signature of a
// stale relative-root trigger: it fails closed with reinstall guidance
// instead of widening the accepted root set.
func ValidateBinding(env Env, triggerName, resourceRoot string) error {
	if env.Trigger != triggerName {
		return fmt.Errorf("trigger binding mismatch: environment trigger %q is not the configured trigger %q", env.Trigger, triggerName)
	}
	if env.HasRelative {
		return fmt.Errorf("root binding mismatch: environment reports relative root %q under watch root %q, but the watch root must be the configured resource root %q itself; the trigger is stale — rerun watchman install", env.RelativeRoot, env.Root, resourceRoot)
	}
	if rootsEquivalent(env.Root, resourceRoot) {
		return nil
	}
	if caseInsensitiveEquivalent(env.Root, resourceRoot) {
		return fmt.Errorf("root binding mismatch: environment root %q and configured resource root %q are the same directory on this case-insensitive volume but spelled differently; correct the configured root to the canonical spelling %q", env.Root, resourceRoot, env.Root)
	}
	return fmt.Errorf("root binding mismatch: environment root %q is not the configured resource root %q (symlinked roots must resolve to the same directory)", env.Root, resourceRoot)
}

// CanonicalRoot cleans a root and resolves symlinks when the path exists
// (the exported form the lifecycle surfaces use for drift comparison).
func CanonicalRoot(root string) string {
	return canonicalRoot(root)
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

// caseInsensitiveEquivalent reports whether two absolute roots differ
// only by letter case — the same directory on a case-insensitive volume
// (darwin/APFS), where symlink resolution does not fold case. It never
// widens acceptance; callers use it to give a case-divergent spelling
// its own actionable refusal instead of a generic root mismatch (the
// D-028 incident's failure class).
func caseInsensitiveEquivalent(a, b string) bool {
	ca, cb := canonicalRoot(a), canonicalRoot(b)
	return ca != cb && strings.EqualFold(ca, cb)
}

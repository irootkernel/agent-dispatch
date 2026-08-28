package hermeskanban

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// Version is a parsed Hermes semantic version from the documented
// `hermes --version` first line (E0-T4 §2):
// `Hermes Agent v<major>.<minor>.<patch> (<build date>)`.
type Version struct {
	Major, Minor, Patch int
	BuildDate           string
}

// String renders the dotted triple used in eligibility comparison.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// MinimumEligibleVersion is the v0.1.5 eligibility floor (ADR-0017,
// HER-011): Hermes 0.19.1 and every later version are probe-eligible;
// there is no fixed maximum and no per-version source allowlist. The
// frozen 0.19.1 interface remains the verified fixture (TST-012); a
// newer Hermes is accepted only through the same probe path.
var MinimumEligibleVersion = Version{Major: 0, Minor: 19, Patch: 1, BuildDate: "2026.7.30"}

// Eligible reports whether v meets the given minimum. The build date is
// recorded evidence, not the gate: the dotted triple is the identity.
func (v Version) Eligible(minimum Version) bool {
	if v.Major != minimum.Major {
		return v.Major > minimum.Major
	}
	if v.Minor != minimum.Minor {
		return v.Minor > minimum.Minor
	}
	return v.Patch >= minimum.Patch
}

// EligibilityFloorText renders the eligibility rule for operator
// messages.
func EligibilityFloorText(minimum Version) string {
	return fmt.Sprintf(">= %s (no maximum; ADR-0017)", minimum)
}

// versionLine matches the documented first line. Version discovery is
// human text only (E0-T4 §2); this pattern is the frozen contract for it
// and an unparsable response fails closed (HER-002).
var versionLine = regexp.MustCompile(`^Hermes Agent v([0-9]+)\.([0-9]+)\.([0-9]+) \(([^)]+)\)$`)

// ParseVersionOutput parses the first line of `hermes --version` output.
// Anything else — including the multi-line installation detail that
// follows the first line — below the first line is ignored; a first line
// that does not match the documented format is an error.
func ParseVersionOutput(output string) (Version, error) {
	first := output
	if i := strings.IndexByte(output, '\n'); i >= 0 {
		first = output[:i]
	}
	first = strings.TrimSpace(first)
	m := versionLine.FindStringSubmatch(first)
	if m == nil {
		return Version{}, fmt.Errorf("unrecognized hermes --version first line %q", truncate(first, 120))
	}
	num := func(s string) (int, error) {
		if len(s) > 9 {
			return 0, fmt.Errorf("version component too large")
		}
		return strconv.Atoi(s)
	}
	major, err := num(m[1])
	if err != nil {
		return Version{}, fmt.Errorf("major: %v", err)
	}
	minor, err := num(m[2])
	if err != nil {
		return Version{}, fmt.Errorf("minor: %v", err)
	}
	patch, err := num(m[3])
	if err != nil {
		return Version{}, fmt.Errorf("patch: %v", err)
	}
	return Version{Major: major, Minor: minor, Patch: patch, BuildDate: m[4]}, nil
}

// ParseMinimumVersion parses a configured minimum-version floor
// (configuration-spec §14) through the one shared domain parser, so
// load-time validation and the run-time gate accept exactly the same
// grammar; the empty value means the 0.19.1 default.
func ParseMinimumVersion(text string) (Version, error) {
	if text == "" {
		return MinimumEligibleVersion, nil
	}
	triple, err := records.ParseVersionTriple(text)
	if err != nil {
		return Version{}, fmt.Errorf("minimum_version %w", err)
	}
	return Version{Major: triple.Major, Minor: triple.Minor, Patch: triple.Patch}, nil
}

// CheckVersionEligible gates one discovered version against the
// configured floor (HER-011: below 0.19.1 is rejected; every later
// version is probe-eligible with no maximum). An eligibility failure
// must fail before any task submission.
func CheckVersionEligible(v, minimum Version) error {
	if v.Eligible(minimum) {
		return nil
	}
	return &VersionUnsupportedError{Found: v.String(), Minimum: minimum.String()}
}

// VersionUnsupportedError is the fail-closed gate result for a Hermes
// version below the configured eligibility floor.
type VersionUnsupportedError struct {
	Found   string
	Minimum string
}

func (e *VersionUnsupportedError) Error() string {
	return fmt.Sprintf("hermes %s is below the eligibility floor %s; route validation fails before any task submission (HER-011)", e.Found, e.Minimum)
}

// Remediation is the actionable operator guidance.
func (e *VersionUnsupportedError) Remediation() string {
	return fmt.Sprintf("install Hermes %s or newer (there is no maximum; compatibility is proven by the public-interface probe) and see docs/architecture-decision-records/0017-capability-probed-hermes-compatibility.md", e.Minimum)
}

package hermeskanban

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed Hermes semantic version from the documented
// `hermes --version` first line (E0-T4 §2):
// `Hermes Agent v<major>.<minor>.<patch> (<build date>)`.
type Version struct {
	Major, Minor, Patch int
	BuildDate           string
}

// String renders the dotted triple used in capability-report comparison.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// SupportedVersions is the exact runtime-verified set (E0-T4 §10: version
// range 0.19.1 exactly; widening requires re-running the probe against
// each additional version and recording it here and in the report).
var SupportedVersions = []Version{
	{Major: 0, Minor: 19, Patch: 1, BuildDate: "2026.7.30"},
}

// Supported reports whether the version is one of the runtime-verified
// Hermes versions. The build date is recorded evidence, not the gate: the
// dotted triple is the identity.
func (v Version) Supported() bool {
	for _, s := range SupportedVersions {
		if v.Major == s.Major && v.Minor == s.Minor && v.Patch == s.Patch {
			return true
		}
	}
	return false
}

// SupportedRangeText renders the supported set for operator messages.
func SupportedRangeText() string {
	parts := make([]string, 0, len(SupportedVersions))
	for _, v := range SupportedVersions {
		parts = append(parts, v.String())
	}
	return strings.Join(parts, ", ")
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

// CheckVersionSupported gates one discovered version against the verified
// set (HER-002/HER-005: an unsupported version must fail before any task
// submission).
func CheckVersionSupported(v Version) error {
	if v.Supported() {
		return nil
	}
	return &VersionUnsupportedError{Found: v.String()}
}

// VersionUnsupportedError is the fail-closed gate result for a Hermes
// version outside the runtime-verified set.
type VersionUnsupportedError struct {
	Found string
}

func (e *VersionUnsupportedError) Error() string {
	return fmt.Sprintf("hermes %s is outside the verified support set (%s); route validation fails before any task submission (HER-002/HER-005)", e.Found, SupportedRangeText())
}

// Remediation is the actionable operator guidance.
func (e *VersionUnsupportedError) Remediation() string {
	return fmt.Sprintf("install a verified Hermes version (%s), re-run the E0-T4 public-interface probe, and refresh the capability report; see docs/integrations/hermes-public-interface-report.md §10", SupportedRangeText())
}

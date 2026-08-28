package records

import (
	"fmt"
	"strconv"
	"strings"
)

// VersionTriple is a strictly parsed `major.minor.patch` version. It is
// the shared parser input for every dotted-version surface — the
// configured Hermes eligibility floor and any future contract floor —
// so load-time validation and run-time gating cannot drift apart on
// acceptance rules.
type VersionTriple struct {
	Major, Minor, Patch int
}

// Less reports whether v sorts before o.
func (v VersionTriple) Less(o VersionTriple) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// String renders the dotted triple.
func (v VersionTriple) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// ParseVersionTriple parses exactly `major.minor.patch`: three bounded
// canonical numeric components with no build suffix, no sign, and no
// leading zeros.
func ParseVersionTriple(text string) (VersionTriple, error) {
	parts := strings.Split(text, ".")
	if len(parts) != 3 {
		return VersionTriple{}, fmt.Errorf("version %q must be major.minor.patch", text)
	}
	var nums [3]int
	for i, part := range parts {
		if part == "" || len(part) > 9 {
			return VersionTriple{}, fmt.Errorf("version %q component %d is not a bounded number", text, i+1)
		}
		n, err := strconv.Atoi(part)
		if err != nil || (len(part) > 1 && part[0] == '0') {
			return VersionTriple{}, fmt.Errorf("version %q component %q is not canonical", text, part)
		}
		nums[i] = n
	}
	return VersionTriple{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
}

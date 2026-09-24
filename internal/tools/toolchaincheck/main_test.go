package main

import "testing"

// TestVersionMatches proves the pinned-toolchain comparison (E9-T7,
// D-023 F4): an unsupported Go version is rejected before any build
// step, and only the exact pin passes, with or without the "go" prefix
// the two sources use.
func TestVersionMatches(t *testing.T) {
	cases := []struct {
		inUse    string
		want     string
		expected bool
	}{
		{"go1.27.1", "1.27.1", true},
		{"1.27.1", "1.27.1", true},
		{"go1.27.1", "go1.27.1", true},
		{"go1.27.0", "1.27.1", false},
		{"go1.26.6", "1.27.1", false},
		{"go1.27", "1.27.1", false},
		{"go1.27.1-rc1", "1.27.1", false},
		{"go1.27.1", "1.27.2", false},
		{"", "1.27.1", false},
	}
	for _, tc := range cases {
		if got := VersionMatches(tc.inUse, tc.want); got != tc.expected {
			t.Errorf("VersionMatches(%q, %q) = %v, want %v", tc.inUse, tc.want, got, tc.expected)
		}
	}
}

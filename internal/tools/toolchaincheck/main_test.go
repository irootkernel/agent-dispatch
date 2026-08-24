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
		{"go1.26.6", "1.26.6", true},
		{"1.26.6", "1.26.6", true},
		{"go1.26.6", "go1.26.6", true},
		{"go1.26.5", "1.26.6", false},
		{"go1.27.0", "1.26.6", false},
		{"go1.26", "1.26.6", false},
		{"go1.26.6-rc1", "1.26.6", false},
		{"go1.26.6", "1.26.7", false},
		{"", "1.26.6", false},
	}
	for _, tc := range cases {
		if got := VersionMatches(tc.inUse, tc.want); got != tc.expected {
			t.Errorf("VersionMatches(%q, %q) = %v, want %v", tc.inUse, tc.want, got, tc.expected)
		}
	}
}

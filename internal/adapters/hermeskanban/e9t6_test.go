package hermeskanban

import "testing"

// TestE9T6RecordedVersionSupportedBoundaries pins the recorded-version
// gate's parsing boundaries (the round-2 residual observation): the
// bare triple passes through unchanged, a foreign format refuses by
// string mismatch rather than accident, and the prefix-adjacent
// 0.19.10 must not match 0.19.1.
func TestE9T6RecordedVersionSupportedBoundaries(t *testing.T) {
	cases := []struct {
		version  string
		expected bool
	}{
		{"0.19.1", true},
		{"0.19.1 (2026.7.30)", true},
		{"0.19.10 (2026.8.1)", false},
		{"0.19.0 (2026.6.1)", false},
		{"0.18.3", false},
		{"Hermes Agent v0.19.1", false},
		{"", false},
		{"  0.19.1", false},
	}
	for _, tc := range cases {
		r := &Report{HermesVersion: tc.version}
		if got := r.RecordedVersionSupported(); got != tc.expected {
			t.Errorf("RecordedVersionSupported(%q) = %v, want %v", tc.version, got, tc.expected)
		}
	}
}

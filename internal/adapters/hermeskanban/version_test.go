package hermeskanban

import (
	"errors"
	"os"
	"testing"
)

// fixtureDir is the frozen E0-T4 public-interface evidence corpus
// (immutable, checksummed in the docs package).
const fixtureDir = "../../../docs/integrations/fixtures/hermes"

func TestParseVersionTable(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		want    Version
		wantErr bool
	}{
		{"documented line", "Hermes Agent v0.19.1 (2026.7.30)", Version{0, 19, 1, "2026.7.30"}, false},
		{"multi-line output keeps first line", "Hermes Agent v0.19.1 (2026.7.30)\nInstall directory: ~/.hermes/hermes-agent\nPython: 3.11.15\n", Version{0, 19, 1, "2026.7.30"}, false},
		{"older patch", "Hermes Agent v0.19.0 (2026.7.01)", Version{0, 19, 0, "2026.7.01"}, false},
		{"newer minor", "Hermes Agent v0.20.0 (2026.8.10)", Version{0, 20, 0, "2026.8.10"}, false},
		{"garbage", "hermes version 19.1", Version{}, true},
		{"empty", "", Version{}, true},
		{"missing build date", "Hermes Agent v0.19.1", Version{}, true},
		{"absurd component", "Hermes Agent v9999999999.0.0 (x)", Version{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseVersionOutput(c.output)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if err == nil && got != c.want {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
}

func TestParseVersionFixture(t *testing.T) {
	raw, err := os.ReadFile(fixtureDir + "/version-output.txt")
	if err != nil {
		t.Fatalf("read frozen version fixture: %v", err)
	}
	v, err := ParseVersionOutput(string(raw))
	if err != nil {
		t.Fatalf("frozen fixture must parse: %v", err)
	}
	if v.String() != "0.19.1" {
		t.Fatalf("fixture version = %q", v.String())
	}
}

func TestVersionEligibilityGate(t *testing.T) {
	eligible := []Version{
		{0, 19, 1, "2026.7.30"},
		{0, 19, 1, "some-other-build"},
		{0, 20, 0, "2026.8.10"},
		{1, 0, 0, "future"},
	}
	for _, v := range eligible {
		if err := CheckVersionEligible(v, MinimumEligibleVersion); err != nil {
			t.Fatalf("version %s at or above the floor must be eligible with no maximum (HER-011): %v", v, err)
		}
	}
	belowFloor := []Version{
		{0, 19, 0, "2026.7.01"},
		{0, 18, 9, "old"},
	}
	for _, v := range belowFloor {
		err := CheckVersionEligible(v, MinimumEligibleVersion)
		var gate *VersionUnsupportedError
		if err == nil {
			t.Fatalf("version %s must fail the gate", v)
		}
		if !errors.As(err, &gate) || gate.Remediation() == "" {
			t.Fatalf("gate error must be VersionUnsupportedError with remediation: %v", err)
		}
	}
	// A declared higher floor tightens the same comparison.
	if err := CheckVersionEligible(Version{0, 19, 5, "x"}, Version{0, 19, 9, "y"}); err == nil {
		t.Fatal("0.19.5 below a declared 0.19.9 floor must fail")
	}
}

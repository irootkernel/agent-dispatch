package main

import (
	"strings"
	"testing"
)

const mod = "github.com/rootkernel/jjukkumi/"

func TestCheckPackagesCatchesViolationFromSubpackage(t *testing.T) {
	_, violations := checkPackages([]goPackage{
		{ImportPath: mod + "internal/domain/records", Imports: []string{mod + "internal/app/ingest"}},
	})
	if len(violations) != 1 || !strings.Contains(violations[0], "internal/domain/records must not import") {
		t.Fatalf("expected one domain->app violation, got %v", violations)
	}
}

// Regression test: the family root packages themselves (internal/ports,
// internal/domain, internal/config) must be covered by the rules, not only
// their subpackages.
func TestCheckPackagesCatchesViolationFromFamilyRoot(t *testing.T) {
	_, violations := checkPackages([]goPackage{
		{ImportPath: mod + "internal/ports", Imports: []string{mod + "internal/app/dispatch"}},
	})
	if len(violations) != 1 {
		t.Fatalf("expected the family-root package to be checked, got %v", violations)
	}
}

func TestCheckPackagesAllowsDocumentedDirections(t *testing.T) {
	count, violations := checkPackages([]goPackage{
		{ImportPath: mod + "internal/adapters/sqlite", Imports: []string{mod + "internal/ports", mod + "internal/domain/records"}},
		{ImportPath: mod + "internal/domain/records", Imports: []string{"fmt", mod + "internal/domain/errors"}},
		{ImportPath: mod + "internal/cli", Imports: []string{mod + "internal/config"}},
		{ImportPath: "cmd/jjukkumi", Imports: []string{mod + "internal/cli"}},
	})
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
	if count != 3 {
		t.Errorf("internal package count = %d, want 3", count)
	}
}

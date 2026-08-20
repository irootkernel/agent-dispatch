package sqlite

import (
	"strings"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/version"
)

// TestVersionMetadataMatchesSchema guards the audit finding F002: the
// version command's reported schema range must track the migration
// baseline this package ships.
func TestVersionMetadataMatchesSchema(t *testing.T) {
	got := strings.SplitN(version.SchemaRange, "-", 2)
	if len(got) != 2 {
		t.Fatalf("schema range %q is not min-max", version.SchemaRange)
	}
	if got[0] != "1" {
		t.Errorf("oldest supported schema = %q, want 1", got[0])
	}
	if got[1] != "2" {
		t.Errorf("newest supported schema = %q, want 2 (migration v2)", got[1])
	}
	if MaxSchemaVersion != 2 {
		t.Errorf("migration baseline drifted to %d; update version.SchemaRange with it", MaxSchemaVersion)
	}
	if version.AdapterVersions()["sqlite"] == "not-implemented (E1-T4)" {
		t.Error("sqlite adapter label must reflect the shipped schema-v1 repositories")
	}
}

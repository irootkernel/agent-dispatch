package sqlite

import (
	"fmt"
	"strings"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/version"
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
	if got[1] != "20" {
		t.Errorf("newest supported schema = %q, want 20 (migration v20)", got[1])
	}
	if MaxSchemaVersion != 20 {
		t.Errorf("migration baseline drifted to %d; update version.SchemaRange with it", MaxSchemaVersion)
	}
	if version.AdapterVersions()["sqlite"] == "not-implemented (E1-T4)" {
		t.Error("sqlite adapter label must reflect the shipped schema-v1 repositories")
	}
	// The operator-facing label must track the migration baseline with
	// its schema-v1..vN prefix, or the version surface silently stops
	// naming newer migrations (the v17/v18 omission found by the E15
	// cold validation).
	wantPrefix := fmt.Sprintf("schema-v1..v%d ", MaxSchemaVersion)
	if label := version.AdapterVersions()["sqlite"]; !strings.HasPrefix(label, wantPrefix) {
		t.Errorf("sqlite adapter label = %q, want prefix %q (update the label with MaxSchemaVersion)", label, wantPrefix)
	}
}

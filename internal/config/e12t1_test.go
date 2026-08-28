package config

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestE12T1DestinationProjectionDeterministicAndAddressed pins the durable
// destination-revision record's two invariants (E12-T1): the projection
// bytes are deterministic across configuration order (FAN-012), and the
// destination revision is exactly the content address of those bytes, so
// the persisted record is self-verifying.
func TestE12T1DestinationProjectionDeterministicAndAddressed(t *testing.T) {
	a, err := Parse(destinationsYAML(t, false))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(destinationsYAML(t, true))
	if err != nil {
		t.Fatal(err)
	}
	routeA, routeB := a.Routes["r1"], b.Routes["r1"]
	alphaA, _ := routeA.DestinationByID("alpha")
	alphaB, _ := routeB.DestinationByID("alpha")
	bytesA, err := DestinationProjectionJSON(a, alphaA)
	if err != nil {
		t.Fatal(err)
	}
	bytesB, err := DestinationProjectionJSON(b, alphaB)
	if err != nil {
		t.Fatal(err)
	}
	if bytesA != bytesB || bytesA == "" {
		t.Fatalf("declaration order must not change the projection bytes:\n%s\nvs\n%s", bytesA, bytesB)
	}
	revA := DestinationRevision(a, routeA, alphaA)
	revB := DestinationRevision(b, routeB, alphaB)
	if revA == "" || revA != revB {
		t.Fatalf("declaration order must not change the destination revision: %q vs %q", revA, revB)
	}
	// The revision is the content address of the persisted bytes: a store
	// holding (revision, projection_json) can verify its own record.
	sum := sha256.Sum256([]byte(bytesA))
	want := "dst-" + hex.EncodeToString(sum[:])
	if revA != want {
		t.Fatalf("destination revision must address the projection bytes: %q vs %q", revA, want)
	}
	// A behavior-affecting edit produces new bytes and a new revision
	// (CON-010): change the workstream on one side only.
	edited, err := Parse(destinationsYAML(t, false))
	if err != nil {
		t.Fatal(err)
	}
	editedRoute := edited.Routes["r1"]
	editTarget, _ := editedRoute.DestinationByID("alpha")
	editTarget.Workstream = "indexing-v2"
	editedRoute.Destinations[0] = editTarget
	editedBytes, err := DestinationProjectionJSON(edited, editTarget)
	if err != nil {
		t.Fatal(err)
	}
	if editedBytes == bytesA || DestinationRevision(edited, editedRoute, editTarget) == revA {
		t.Fatal("a behavior-affecting edit must change the projection and revision")
	}
}

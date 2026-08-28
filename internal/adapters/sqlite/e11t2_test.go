package sqlite

import (
	"context"
	"testing"
)

// TestE11T2EmptyFingerprintReAcknowledgementPreservesBinding proves the
// schema-v11 outage arm: a re-acknowledgement carrying an empty
// capability fingerprint (the enable gate deferred the live probe for
// an unavailable target) preserves the previously accepted binding
// instead of silently disabling the submit-time executable-change
// block.
func TestE11T2EmptyFingerprintReAcknowledgementPreservesBinding(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.SetRouteActivation(ctx, "wiki-maintenance", "enabled", "route-rev-2", "cap:0123456789abcdef", now()); err != nil {
		t.Fatal(err)
	}
	snap, err := s.LoadRouteState(ctx, "wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if snap.CapabilityFingerprint != "cap:0123456789abcdef" {
		t.Fatalf("the acknowledged binding must persist, got %q", snap.CapabilityFingerprint)
	}
	// The outage re-acknowledgement: empty fingerprint, new revision.
	if err := s.SetRouteActivation(ctx, "wiki-maintenance", "enabled", "route-rev-3", "", now()); err != nil {
		t.Fatal(err)
	}
	snap, err = s.LoadRouteState(ctx, "wiki-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if snap.CapabilityFingerprint != "cap:0123456789abcdef" {
		t.Fatalf("an empty-fingerprint re-acknowledgement must preserve the binding, got %q", snap.CapabilityFingerprint)
	}
	if snap.AcknowledgedRevision != "route-rev-3" {
		t.Fatalf("the revision must still advance, got %q", snap.AcknowledgedRevision)
	}
}

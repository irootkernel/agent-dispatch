package syncmanagement

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestFrozenPolicyAndLifecycle(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/sync-management-v1/policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Limits map[string]int `json:"limits"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	for key, got := range map[string]int{
		"preparation_ttl_seconds": DraftTTLSeconds, "expired_draft_retention_seconds": ExpiredRetentionSeconds,
		"prepared_data_limit": DataDraftLimit, "prepared_control_limit": ControlDraftLimit,
		"unresolved_data_min": DataQueueMin, "unresolved_data_max": DataQueueMax,
		"unresolved_control_limit": ControlQueueLimit, "terminal_data_limit": DataTerminalLimit,
		"terminal_control_limit": ControlTerminalLimit, "association_limit": AssociationLimit,
		"cumulative_attempts": AttemptLimit, "claim_lease_seconds": LeaseSeconds,
		"execution_deadline_seconds": ExecutionSeconds, "call_deadline_seconds": CallSeconds,
		"reason_bytes": ReasonBytes, "minimum_terminal_retention_days": MinimumRetentionDays,
	} {
		if want, ok := policy.Limits[key]; !ok || got != want {
			t.Errorf("%s=%d, frozen %d (present=%v)", key, got, want, ok)
		}
	}
	raw, err = os.ReadFile("../../../docs/contracts/sync-management-v1/lifecycle.json")
	if err != nil {
		t.Fatal(err)
	}
	var lifecycle struct {
		States      []string            `json:"states"`
		Transitions map[string][]string `json:"transitions"`
	}
	if err := json.Unmarshal(raw, &lifecycle); err != nil {
		t.Fatal(err)
	}
	for _, from := range append(lifecycle.States, "unknown") {
		for _, to := range append(lifecycle.States, "unknown") {
			if got, want := CanTransition(from, to), slices.Contains(lifecycle.Transitions[from], to); got != want {
				t.Errorf("%s -> %s: %v, frozen %v", from, to, got, want)
			}
		}
	}
}

func TestFingerprintMatchesFrozenVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/sync-management-v1/digest-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Vectors []struct {
			Domain string          `json:"domain"`
			Value  json.RawMessage `json:"value"`
			Digest string          `json:"digest"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, v := range fixture.Vectors {
		got, err := Digest(v.Domain, v.Value)
		if err != nil || got != v.Digest {
			t.Fatalf("domain %s: %s %v want %s", v.Domain, got, err, v.Digest)
		}
		if v.Domain == "agent-dispatch.sync-management.request/v1" {
			var b Binding
			if err := json.Unmarshal(v.Value, &b); err != nil {
				t.Fatal(err)
			}
			got, err := b.Fingerprint()
			if err != nil || got != v.Digest {
				t.Fatalf("typed projection: %s %v", got, err)
			}
		}
		checked++
	}
	if checked != 7 {
		t.Fatalf("checked %d frozen vectors", checked)
	}
}

func TestBindingsAndOutcomeRefuseFalseIdentity(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	b := Binding{GroupID: "wiki", InstanceID: "node-a", StateIncarnationID: "epoch-a", Action: "sync-now", PolicyRevision: d, ConfigRevision: d}
	fp, err := b.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	changed := b
	changed.StateIncarnationID = "epoch-b"
	other, _ := changed.Fingerprint()
	if fp == other {
		t.Fatal("incarnation missing from fingerprint")
	}
	for _, change := range []func(*Binding){func(b *Binding) { b.Action = "publish" }, func(b *Binding) { b.GroupID = "wiki\n" }, func(b *Binding) { b.InstanceID = "../node" }, func(b *Binding) { b.PolicyRevision = "SHA256:" + strings.Repeat("a", 64) }, func(b *Binding) { b.Action = "retry" }, func(b *Binding) { b.Action = "pause" }} {
		bad := b
		change(&bad)
		if _, err := bad.Fingerprint(); err == nil {
			t.Fatalf("accepted invalid binding %+v", bad)
		}
	}
	if err := (Outcome{Status: "complete"}).Validate("verify-all"); err == nil {
		t.Fatal("verification result without pinned identity")
	}
	if err := (Outcome{Status: "failed", Reason: strings.Repeat("한", 86)}).Validate("sync-now"); err == nil {
		t.Fatal("UTF-8 byte ceiling ignored")
	}
	id, err := NewHandle()
	if err != nil || !ValidHandle(id) {
		t.Fatalf("generated handle %q: %v", id, err)
	}
}

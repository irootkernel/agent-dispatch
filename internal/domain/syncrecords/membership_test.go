package syncrecords

import (
	"errors"
	"testing"
)

func TestMembershipPlansCoverClosedChangeVocabulary(t *testing.T) {
	binding := testBinding()
	desired := testMembers()
	bootstrap, err := NewPlan("bootstrap", "", nil, nil, desired, binding)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.PlanID == "" || bootstrap.ProposedMembership.Mode != "normal" {
		t.Fatalf("invalid bootstrap: %+v", bootstrap)
	}
	predecessor := "1111111111111111111111111111111111111111"
	current := bootstrap.ProposedMembership
	current.Predecessor = nil

	cases := []struct {
		name, kind, target string
		desired            []ActiveMember
		mode               string
	}{
		{"endpoint", "endpoint_update", "node-a", mutate(desired, "node-a", func(m *ActiveMember) { m.Endpoint = "https://node-a-next.example.ts.net" }), "normal"},
		{"endpoint private port", "endpoint_update", "node-a", mutate(desired, "node-a", func(m *ActiveMember) { m.Endpoint = "https://node-a.example.ts.net:8448" }), "normal"},
		{"key", "key_rotation", "node-a", mutate(desired, "node-a", func(m *ActiveMember) { m.PublisherKey = fingerprint('D'); m.StateIncarnationID = "node-a-0002" }), "normal"},
		{"incarnation", "incarnation_registration", "node-a", mutate(desired, "node-a", func(m *ActiveMember) { m.StateIncarnationID = "node-a-0002" }), "normal"},
		{"replacement", "replacement", "node-a", []ActiveMember{desired[1], {InstanceID: "node-c", StateIncarnationID: "node-c-0001", PublisherKey: fingerprint('D'), Endpoint: "https://node-c.example.ts.net"}}, "normal"},
		{"retirement", "retirement", "node-a", desired, "blocked_emergency"},
		{"revocation", "emergency_revocation", "node-a", desired, "blocked_emergency"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := NewPlan(tc.kind, tc.target, &current, &predecessor, tc.desired, binding)
			if err != nil {
				t.Fatal(err)
			}
			if plan.ProposedMembership.Mode != tc.mode {
				t.Fatalf("mode=%q want %q", plan.ProposedMembership.Mode, tc.mode)
			}
			if err := ValidateTransition(current, plan); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBlockedMembershipRequiresExplicitReplacementRecovery(t *testing.T) {
	binding := testBinding()
	desired := testMembers()
	root, _ := NewPlan("bootstrap", "", nil, nil, desired, binding)
	pred := "1111111111111111111111111111111111111111"
	blockedPlan, err := NewPlan("emergency_revocation", "node-b", &root.ProposedMembership, &pred, desired, binding)
	if err != nil {
		t.Fatal(err)
	}
	blocked := blockedPlan.ProposedMembership
	next := "2222222222222222222222222222222222222222"
	replacement := mutate(desired, "node-b", func(m *ActiveMember) { m.StateIncarnationID = "node-b-0002"; m.PublisherKey = fingerprint('D') })
	recovery, err := NewPlan("replacement", "node-b", &blocked, &next, replacement, binding)
	if err != nil {
		t.Fatal(err)
	}
	if recovery.ProposedMembership.Mode != "normal" || len(recovery.ProposedMembership.ActiveMembers) != 2 {
		t.Fatalf("recovery did not restore pair: %+v", recovery.ProposedMembership)
	}
}

func TestPlanIDAndStrictDecodeRejectTampering(t *testing.T) {
	plan, err := NewPlan("bootstrap", "", nil, nil, testMembers(), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := CanonicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlan(raw)
	if err != nil || decoded.PlanID != plan.PlanID {
		t.Fatalf("decode: %+v %v", decoded, err)
	}
	duplicate := []byte(`{"schema_version":"agent-dispatch.sync-membership-plan/v1","schema_version":"agent-dispatch.sync-membership-plan/v1"}`)
	if _, err := DecodePlan(duplicate); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("duplicate key accepted: %v", err)
	}
	plan.PlanID = "membership-tampered"
	if _, err := CanonicalPlan(plan); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("tampered plan id accepted: %v", err)
	}
}

func TestRemovedKeyFirstSeenRequiresAdministratorCheckpoint(t *testing.T) {
	at := Membership{ActiveMembers: testMembers()}
	current := Membership{ActiveMembers: testMembers()[1:]}
	key := testMembers()[0].PublisherKey
	if err := AuthorizePublisher(at, current, key, false, false); !errors.Is(err, ErrRemovedKeyFirstSeen) {
		t.Fatalf("first-seen removed key accepted: %v", err)
	}
	if err := AuthorizePublisher(at, current, key, true, false); err != nil {
		t.Fatalf("historically verified evidence rejected: %v", err)
	}
	if err := AuthorizePublisher(at, current, key, false, true); err != nil {
		t.Fatalf("administrator checkpoint rejected: %v", err)
	}
}

func testBinding() Binding {
	return Binding{GroupID: "wiki-pair", ContentRef: "refs/heads/wiki-sync", ContentBinding: "sha256:" + repeat('d', 64), ContractDigest: "sha256:" + repeat('e', 64), AdministratorKey: fingerprint('A')}
}

func testMembers() []ActiveMember {
	return []ActiveMember{
		{InstanceID: "node-a", StateIncarnationID: "node-a-0001", PublisherKey: fingerprint('B'), Endpoint: "https://node-a.example.ts.net"},
		{InstanceID: "node-b", StateIncarnationID: "node-b-0001", PublisherKey: fingerprint('C'), Endpoint: "https://node-b.example.ts.net"},
	}
}

func TestMembershipRejectsInvalidEndpointPorts(t *testing.T) {
	for _, endpoint := range []string{
		"https://node-a.example.ts.net:0",
		"https://node-a.example.ts.net:65536",
		"https://node-a.example.ts.net:",
		"https://node-a.example.ts.net:01",
		"https://user@node-a.example.ts.net:8448",
	} {
		members := testMembers()
		members[0].Endpoint = endpoint
		if _, err := NewPlan("bootstrap", "", nil, nil, members, testBinding()); err == nil {
			t.Errorf("invalid signed membership endpoint accepted: %q", endpoint)
		}
	}
}

func fingerprint(r byte) string { return "SHA256:" + repeat(r, 42) + "A" }
func repeat(r byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}

func mutate(in []ActiveMember, id string, fn func(*ActiveMember)) []ActiveMember {
	out := append([]ActiveMember(nil), in...)
	for i := range out {
		if out[i].InstanceID == id {
			fn(&out[i])
		}
	}
	return out
}

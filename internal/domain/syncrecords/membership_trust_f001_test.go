package syncrecords

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMembershipRequiredHistoricalArray(t *testing.T) {
	plan, err := NewPlan("bootstrap", "", nil, nil, testMembers(), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := CanonicalMembership(plan.ProposedMembership)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"missing", "null", "[]"} {
		t.Run(value, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if value == "missing" {
				delete(fields, "historical_members")
			} else {
				fields["historical_members"] = json.RawMessage(value)
			}
			membershipRaw, _ := json.Marshal(fields)
			decoded, err := DecodeMembership(membershipRaw)
			if value != "[]" {
				if !errors.Is(err, ErrInvalidRecord) {
					t.Fatalf("invalid historical_members accepted: %v", err)
				}
			} else {
				canonical, canonicalErr := CanonicalMembership(decoded)
				if err != nil || canonicalErr != nil || !bytes.Equal(raw, canonical) {
					t.Fatalf("empty array changed membership: %v %v", err, canonicalErr)
				}
			}
			planRaw, _ := CanonicalPlan(plan)
			var planFields map[string]json.RawMessage
			if err := json.Unmarshal(planRaw, &planFields); err != nil {
				t.Fatal(err)
			}
			planFields["proposed_membership"] = membershipRaw
			invalidPlan, _ := json.Marshal(planFields)
			decodedPlan, err := DecodePlan(invalidPlan)
			if value != "[]" {
				if !errors.Is(err, ErrInvalidRecord) {
					t.Fatalf("invalid proposed historical_members accepted: %v", err)
				}
			} else if err != nil || decodedPlan.PlanID != plan.PlanID {
				t.Fatalf("empty array changed plan identity: %v", err)
			}
		})
	}
}

func TestMembershipRejectsInvalidGitRefs(t *testing.T) {
	plan, err := NewPlan("bootstrap", "", nil, nil, testMembers(), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := CanonicalMembership(plan.ProposedMembership)
	for _, ref := range []string{"refs/heads/wiki.lock", "refs/heads/wiki//main", "refs/heads/wiki\nmain", "refs/heads/.wiki", "refs/heads/wiki/part.lock", "refs/heads/wiki."} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		fields["content_ref"], _ = json.Marshal(ref)
		malformed, _ := json.Marshal(fields)
		if _, err := DecodeMembership(malformed); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("invalid Git ref %q accepted: %v", ref, err)
		}
	}
	plan.ProposedMembership.ContentRef = "refs/heads/wiki/main-v1.0"
	raw, err = CanonicalMembership(plan.ProposedMembership)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeMembership(raw); err != nil {
		t.Fatalf("valid nested Git ref rejected: %v", err)
	}
}

func TestDecodeMembershipRequiredPredecessor(t *testing.T) {
	parent := strings.Repeat("1", 40)
	for _, predecessor := range []*string{nil, &parent} {
		plan, err := NewPlan("bootstrap", "", nil, nil, testMembers(), testBinding())
		if err != nil {
			t.Fatal(err)
		}
		document := plan.ProposedMembership
		document.Predecessor = predecessor
		raw, err := CanonicalMembership(document)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeMembership(raw)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := CanonicalMembership(decoded)
		if err != nil || !bytes.Equal(raw, canonical) {
			t.Fatalf("predecessor or canonical bytes changed: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, "predecessor")
		omitted, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeMembership(omitted); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("missing required predecessor accepted: %v", err)
		}
	}
}

func TestDecodePlanTrustF001RequiredPredecessors(t *testing.T) {
	bootstrap, err := NewPlan("bootstrap", "", nil, nil, testMembers(), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	predecessor := "1111111111111111111111111111111111111111"
	desired := mutate(testMembers(), "node-a", func(m *ActiveMember) {
		m.Endpoint = "https://node-a-next.example.ts.net"
	})
	update, err := NewPlan("endpoint_update", "node-a", &bootstrap.ProposedMembership, &predecessor, desired, testBinding())
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []MembershipPlan{bootstrap, update} {
		t.Run(plan.ChangeKind, func(t *testing.T) {
			raw, err := CanonicalPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("explicit predecessors", func(t *testing.T) {
				decoded, err := DecodePlan(raw)
				if err != nil {
					t.Fatal(err)
				}
				canonical, err := CanonicalPlan(decoded)
				if err != nil {
					t.Fatal(err)
				}
				if decoded.PlanID != plan.PlanID || !bytes.Equal(canonical, raw) {
					t.Fatal("decoding changed canonical plan identity or bytes")
				}
				if plan.ChangeKind == "bootstrap" && (decoded.ExpectedPredecessor != nil || decoded.ProposedMembership.Predecessor != nil) {
					t.Fatal("bootstrap predecessors must remain null")
				}
			})
			for _, tc := range []struct {
				name                    string
				omitExpected, omitInner bool
			}{
				{"missing expected_predecessor", true, false},
				{"missing proposed_membership.predecessor", false, true},
				{"missing both predecessors", true, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(raw, &fields); err != nil {
						t.Fatal(err)
					}
					if tc.omitExpected {
						delete(fields, "expected_predecessor")
					}
					if tc.omitInner {
						var membership map[string]json.RawMessage
						if err := json.Unmarshal(fields["proposed_membership"], &membership); err != nil {
							t.Fatal(err)
						}
						delete(membership, "predecessor")
						fields["proposed_membership"], err = json.Marshal(membership)
						if err != nil {
							t.Fatal(err)
						}
					}
					omitted, err := json.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := DecodePlan(omitted); !errors.Is(err, ErrInvalidRecord) {
						t.Fatalf("omitted required predecessor accepted: %v", err)
					}
				})
			}
		})
	}
}

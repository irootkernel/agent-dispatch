package syncrecords

import (
	"strings"
	"testing"
	"time"
)

func validVerificationForTest() Verification {
	revision, target := strings.Repeat("1", 40), strings.Repeat("2", 40)
	scope, contract := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	nodes := []VerificationNode{}
	for _, id := range []string{"node-a", "node-b"} {
		nodes = append(nodes, VerificationNode{
			InstanceID: id, StateIncarnationID: id + "-0001", MembershipRevision: revision,
			ContentRef: "refs/heads/wiki-sync", TargetCommit: target, ScopeDigest: scope,
			ContractDigest: contract, Nonce: "0123456789abcdef", State: "applied",
			EvidenceGeneration: time.Now().UnixNano(), EvidenceAgeSeconds: 1,
			MembershipCurrent: true, EvidenceFresh: true,
		})
	}
	return Verification{
		SchemaVersion: VerificationSchema, VerificationID: "verification-test", GroupID: "wiki-pair",
		MembershipRevision: revision, ContentRef: "refs/heads/wiki-sync", TargetCommit: target,
		ScopeDigest: scope, ContractDigest: contract, Phase: "finished", Result: "complete",
		ExpectedNodes: []ExpectedNode{{InstanceID: "node-a", StateIncarnationID: "node-a-0001"}, {InstanceID: "node-b", StateIncarnationID: "node-b-0001"}},
		Nodes:         nodes, Fence: 1,
	}
}

func TestVerificationRejectsUnboundOrUnsafePair(t *testing.T) {
	valid := validVerificationForTest()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid pair: %v", err)
	}
	tests := []struct {
		name   string
		change func(*Verification)
	}{
		{"duplicate expected identity", func(v *Verification) { v.ExpectedNodes[1] = v.ExpectedNodes[0] }},
		{"obsolete incarnation", func(v *Verification) { v.Nodes[1].StateIncarnationID = "node-b-old1" }},
		{"different membership", func(v *Verification) { v.Nodes[1].MembershipRevision = strings.Repeat("3", 40) }},
		{"different target", func(v *Verification) { v.Nodes[1].TargetCommit = strings.Repeat("3", 40) }},
		{"different scope", func(v *Verification) { v.Nodes[1].ScopeDigest = "sha256:" + strings.Repeat("d", 64) }},
		{"different contract", func(v *Verification) { v.Nodes[1].ContractDigest = "sha256:" + strings.Repeat("c", 64) }},
		{"short nonce", func(v *Verification) { v.Nodes[1].Nonce = "short" }},
		{"false freshness", func(v *Verification) { v.Nodes[1].EvidenceAgeSeconds = 301 }},
		{"dirty complete", func(v *Verification) { v.Nodes[1].GovernedDirty = true }},
		{"pending complete", func(v *Verification) { v.Nodes[1].PendingWork = true }},
		{"uncertain complete", func(v *Verification) { v.Nodes[1].Uncertain = true }},
		{"missing peer", func(v *Verification) { v.Nodes = v.Nodes[:1] }},
		{"incomplete without retention", func(v *Verification) { v.Result = "incomplete" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := validVerificationForTest()
			tc.change(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("unsafe verification was accepted")
			}
		})
	}
}

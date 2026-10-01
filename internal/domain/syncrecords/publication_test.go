package syncrecords

import (
	"encoding/json"
	"testing"
)

func TestPublicationIdentityIsStableAndManifestIsNotSelfReferential(t *testing.T) {
	binding := PublicationBinding{GroupID: "wiki-pair", Publisher: "node-a", StateIncarnationID: "node-a-0001", MembershipRevision: "1111111111111111111111111111111111111111", ContentRef: "refs/heads/wiki-sync", BaseCommit: "2222222222222222222222222222222222222222", ScopeDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContractDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SourceRevision: 7, ReceiptIDs: []string{"receipt-b", "receipt-a"}}
	files := []SnapshotFile{{Path: "B.md", Digest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}, {Path: "A.md", Digest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}}
	first, err := NewPublication(binding, files)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPublication(binding, []SnapshotFile{files[1], files[0]})
	if err != nil {
		t.Fatal(err)
	}
	if first.PublicationID != second.PublicationID || first.SnapshotDigest != second.SnapshotDigest {
		t.Fatalf("identity is order-sensitive: %#v %#v", first, second)
	}
	raw, err := CanonicalPublication(first)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if _, ok := object["candidate_commit"]; ok {
		t.Fatal("prepared manifest must not contain its own commit ID")
	}
}

func TestCheckpointPlanBindsEveryReviewedInput(t *testing.T) {
	p, err := NewCheckpointPlan("wiki-pair", "conflict_resolution", "1111111111111111111111111111111111111111", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "2222222222222222222222222222222222222222", "3333333333333333333333333333333333333333", "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := CanonicalCheckpointPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCheckpointPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.PlanID != p.PlanID {
		t.Fatal("plan identity changed")
	}
	p.ProposedCheckpoint.TargetCommit = "4444444444444444444444444444444444444444"
	if err := p.Validate(); err == nil {
		t.Fatal("mutated target retained a valid plan identity")
	}
}

func TestCheckpointPlanMatchesLexicalContractIDs(t *testing.T) {
	p, err := NewCheckpointPlan("wiki-pair", "conflict_resolution", "1111111111111111111111111111111111111111", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "2222222222222222222222222222222222222222", "3333333333333333333333333333333333333333", "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	// Independent vectors use lexical keys at every depth and domain/NUL/JSON/LF.
	if p.ProposedCheckpoint.CheckpointID != "checkpoint-5e2a2157389087eb44e8f06441637a1f" {
		t.Fatalf("checkpoint_id=%s", p.ProposedCheckpoint.CheckpointID)
	}
	if p.PlanID != "checkpoint-plan-b1baf0e277fc12f5b45a5411329e5fd5" {
		t.Fatalf("plan_id=%s", p.PlanID)
	}
	raw, err := CanonicalCheckpointPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCheckpointPlan(raw); err != nil {
		t.Fatalf("contract identity rejected: %v", err)
	}
}

func TestFramedDigestCanonicalizesNestedKeysWithoutRounding(t *testing.T) {
	projection := struct {
		Z int64          `json:"z"`
		A []SnapshotFile `json:"a"`
	}{
		Z: 9007199254740993,
		A: []SnapshotFile{
			{Path: "눈<&>\u2028.md", Digest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
			{Path: "A.md", Digest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		},
	}
	got, err := framedDigest("agent-dispatch.sync-snapshot/v1", projection)
	if err != nil {
		t.Fatal(err)
	}
	// The independent vector preserves array order, exact integers and contract escaping.
	const want = "sha256:af659d7a463eda976dbb8b5bbcd748fde03d4a9787680274fbaac76b329daf79"
	if got != want {
		t.Fatalf("digest=%s want=%s", got, want)
	}
}

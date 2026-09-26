package syncrecords

import (
	"fmt"
	"time"
)

const (
	VerificationSchema    = "agent-dispatch.sync-verification/v1"
	MaxEvidenceAgeSeconds = 300
)

func EvidenceAgeSecondsSince(elapsed time.Duration) int64 {
	if elapsed <= 0 {
		return 0
	}
	return int64((elapsed + time.Second - 1) / time.Second)
}

type ExpectedNode struct {
	InstanceID         string `json:"instance_id"`
	StateIncarnationID string `json:"state_incarnation_id"`
}

type VerificationNode struct {
	InstanceID         string `json:"instance_id"`
	StateIncarnationID string `json:"state_incarnation_id"`
	MembershipRevision string `json:"membership_revision"`
	ContentRef         string `json:"content_ref"`
	TargetCommit       string `json:"target_commit"`
	ScopeDigest        string `json:"scope_digest"`
	ContractDigest     string `json:"contract_digest"`
	Nonce              string `json:"nonce"`
	State              string `json:"state"`
	EvidenceGeneration int64  `json:"evidence_generation"`
	EvidenceAgeSeconds int64  `json:"evidence_age_seconds"`
	GovernedDirty      bool   `json:"governed_dirty"`
	PendingWork        bool   `json:"pending_work"`
	MembershipCurrent  bool   `json:"membership_current"`
	EvidenceFresh      bool   `json:"evidence_fresh"`
	Uncertain          bool   `json:"uncertain"`
}

func VerificationNodeFromStatus(r StatusResponse) VerificationNode {
	return VerificationNode{
		InstanceID: r.Responder, StateIncarnationID: r.StateIncarnationID,
		MembershipRevision: r.MembershipRevision, ContentRef: r.ContentRef,
		TargetCommit: r.TargetCommit, ScopeDigest: r.ScopeDigest, ContractDigest: r.ContractDigest,
		Nonce: r.Nonce, State: r.State, EvidenceGeneration: r.EvidenceGeneration,
		EvidenceAgeSeconds: r.EvidenceAgeSeconds, GovernedDirty: r.GovernedDirty,
		PendingWork: r.PendingWork, MembershipCurrent: r.MembershipCurrent,
		EvidenceFresh: r.EvidenceAgeSeconds <= MaxEvidenceAgeSeconds, Uncertain: r.Uncertain,
	}
}

func (v Verification) CleanPair() bool {
	if len(v.Nodes) != 2 {
		return false
	}
	for _, node := range v.Nodes {
		if node.State != "applied" || node.GovernedDirty || node.PendingWork || !node.MembershipCurrent || !node.EvidenceFresh || node.Uncertain {
			return false
		}
	}
	return true
}

type Verification struct {
	SchemaVersion       string             `json:"schema_version"`
	VerificationID      string             `json:"verification_id"`
	GroupID             string             `json:"group_id"`
	MembershipRevision  string             `json:"membership_revision"`
	ContentRef          string             `json:"content_ref"`
	TargetCommit        string             `json:"target_commit"`
	ScopeDigest         string             `json:"scope_digest"`
	ContractDigest      string             `json:"contract_digest"`
	Phase               string             `json:"phase"`
	Result              string             `json:"result"`
	ExpectedNodes       []ExpectedNode     `json:"expected_nodes"`
	Nodes               []VerificationNode `json:"nodes"`
	Fence               int64              `json:"fence"`
	RetainUntilResolved bool               `json:"retain_until_resolved"`
}

func (v Verification) Validate() error {
	if v.SchemaVersion != VerificationSchema || !recordID(v.VerificationID) || !identityPattern.MatchString(v.GroupID) ||
		!oidPattern.MatchString(v.MembershipRevision) || !validContentRef(v.ContentRef) ||
		!oidPattern.MatchString(v.TargetCommit) || !digestPattern.MatchString(v.ScopeDigest) ||
		!digestPattern.MatchString(v.ContractDigest) || v.Fence < 1 || len(v.ExpectedNodes) != 2 || len(v.Nodes) > 2 {
		return fmt.Errorf("%w: invalid verification target", ErrInvalidRecord)
	}
	if v.ExpectedNodes[0].InstanceID == v.ExpectedNodes[1].InstanceID || v.ExpectedNodes[0].StateIncarnationID == v.ExpectedNodes[1].StateIncarnationID {
		return fmt.Errorf("%w: duplicate expected verification node", ErrInvalidRecord)
	}
	expected := make(map[string]string, 2)
	for _, node := range v.ExpectedNodes {
		if !identityPattern.MatchString(node.InstanceID) || !incarnationPattern.MatchString(node.StateIncarnationID) {
			return fmt.Errorf("%w: invalid expected verification node", ErrInvalidRecord)
		}
		expected[node.InstanceID] = node.StateIncarnationID
	}
	seen := make(map[string]bool, 2)
	for _, node := range v.Nodes {
		if seen[node.InstanceID] || expected[node.InstanceID] != node.StateIncarnationID ||
			node.MembershipRevision != v.MembershipRevision || node.ContentRef != v.ContentRef ||
			node.TargetCommit != v.TargetCommit || node.ScopeDigest != v.ScopeDigest || node.ContractDigest != v.ContractDigest ||
			len(node.Nonce) < 16 || len(node.Nonce) > 128 || node.EvidenceGeneration < 1 || node.EvidenceAgeSeconds < 0 ||
			node.EvidenceFresh != (node.EvidenceAgeSeconds <= MaxEvidenceAgeSeconds) {
			return fmt.Errorf("%w: verification node is not bound to target", ErrInvalidRecord)
		}
		seen[node.InstanceID] = true
	}
	if v.Phase != "finished" || (v.Result != "complete" && v.Result != "incomplete" && v.Result != "target_changed" && v.Result != "blocked" && v.Result != "expired") {
		return fmt.Errorf("%w: invalid verification outcome", ErrInvalidRecord)
	}
	if v.Result == "complete" {
		if !v.CleanPair() || v.RetainUntilResolved {
			return fmt.Errorf("%w: complete verification lacks a clean pair", ErrInvalidRecord)
		}
	} else if !v.RetainUntilResolved {
		return fmt.Errorf("%w: incomplete verification must retain its result", ErrInvalidRecord)
	}
	return nil
}

package syncrecords

import "fmt"

const (
	StatusRequestSchema  = "agent-dispatch.sync-status-request/v1"
	StatusResponseSchema = "agent-dispatch.sync-status-response/v1"
)

// StatusRequest binds a fresh peer observation to one proposed pair target.
type StatusRequest struct {
	SchemaVersion      string `json:"schema_version"`
	GroupID            string `json:"group_id"`
	Sender             string `json:"sender"`
	Receiver           string `json:"receiver"`
	MembershipRevision string `json:"membership_revision"`
	ContentRef         string `json:"content_ref"`
	TargetCommit       string `json:"target_commit"`
	ScopeDigest        string `json:"scope_digest"`
	ContractDigest     string `json:"contract_digest"`
	Nonce              string `json:"nonce"`
}

func (r StatusRequest) Validate() error {
	if r.SchemaVersion != StatusRequestSchema || !identityPattern.MatchString(r.GroupID) ||
		!identityPattern.MatchString(r.Sender) || !identityPattern.MatchString(r.Receiver) || r.Sender == r.Receiver ||
		!oidPattern.MatchString(r.MembershipRevision) || !validContentRef(r.ContentRef) ||
		!oidPattern.MatchString(r.TargetCommit) || !digestPattern.MatchString(r.ScopeDigest) ||
		!digestPattern.MatchString(r.ContractDigest) || len(r.Nonce) < 16 || len(r.Nonce) > 128 {
		return fmt.Errorf("%w: invalid peer status request", ErrInvalidRecord)
	}
	return nil
}

func DecodeStatusRequest(raw []byte) (StatusRequest, error) {
	var r StatusRequest
	if len(raw) > 16<<10 {
		return r, fmt.Errorf("%w: peer status request exceeds 16 KiB", ErrInvalidRecord)
	}
	if err := decodeStrict(raw, &r); err != nil {
		return r, err
	}
	return r, r.Validate()
}

// StatusResponse reports one observation. Verification decides whether two
// observations establish pair convergence; the endpoint cannot claim that.
type StatusResponse struct {
	SchemaVersion      string `json:"schema_version"`
	GroupID            string `json:"group_id"`
	Responder          string `json:"responder"`
	StateIncarnationID string `json:"state_incarnation_id"`
	MembershipRevision string `json:"membership_revision"`
	ContentRef         string `json:"content_ref"`
	TargetCommit       string `json:"target_commit"`
	ScopeDigest        string `json:"scope_digest"`
	ContractDigest     string `json:"contract_digest"`
	Nonce              string `json:"nonce"`
	EvidenceGeneration int64  `json:"evidence_generation"`
	EvidenceAgeSeconds int64  `json:"evidence_age_seconds"`
	State              string `json:"state"`
	GovernedDirty      bool   `json:"governed_dirty"`
	PendingWork        bool   `json:"pending_work"`
	MembershipCurrent  bool   `json:"membership_current"`
	Uncertain          bool   `json:"uncertain"`
}

func (r StatusResponse) Validate() error {
	states := map[string]bool{"applied": true, "behind": true, "ahead": true, "local_dirty": true, "joining": true, "paused": true, "blocked": true, "unknown": true}
	if r.SchemaVersion != StatusResponseSchema || !identityPattern.MatchString(r.GroupID) ||
		!identityPattern.MatchString(r.Responder) || !incarnationPattern.MatchString(r.StateIncarnationID) ||
		!oidPattern.MatchString(r.MembershipRevision) || !validContentRef(r.ContentRef) ||
		!oidPattern.MatchString(r.TargetCommit) || !digestPattern.MatchString(r.ScopeDigest) ||
		!digestPattern.MatchString(r.ContractDigest) || len(r.Nonce) < 16 || len(r.Nonce) > 128 ||
		r.EvidenceGeneration < 1 || r.EvidenceAgeSeconds < 0 || !states[r.State] {
		return fmt.Errorf("%w: invalid peer status response", ErrInvalidRecord)
	}
	return nil
}

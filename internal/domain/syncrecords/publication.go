package syncrecords

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

const (
	PublicationSchema    = "agent-dispatch.sync-publication/v1"
	CheckpointSchema     = "agent-dispatch.sync-checkpoint/v1"
	CheckpointPlanSchema = "agent-dispatch.sync-checkpoint-plan/v1"
	NudgeSchema          = "agent-dispatch.sync-nudge/v1"
)

// Publication is the immutable prepared evidence embedded in a content
// commit. CandidateCommit and RemoteCommit deliberately remain absent from
// the embedded form because a commit cannot contain its own object ID.
type Publication struct {
	SchemaVersion       string   `json:"schema_version"`
	PublicationID       string   `json:"publication_id"`
	GroupID             string   `json:"group_id"`
	Publisher           string   `json:"publisher"`
	StateIncarnationID  string   `json:"state_incarnation_id"`
	MembershipRevision  string   `json:"membership_revision"`
	ContentRef          string   `json:"content_ref"`
	SourceRevision      int64    `json:"source_revision"`
	BaseCommit          string   `json:"base_commit"`
	SnapshotDigest      string   `json:"snapshot_digest"`
	ScopeDigest         string   `json:"scope_digest"`
	ContractDigest      string   `json:"contract_digest"`
	ReceiptIDs          []string `json:"receipt_ids"`
	CauseID             string   `json:"cause_id"`
	State               string   `json:"state"`
	Reason              string   `json:"reason"`
	Attempts            int      `json:"attempts"`
	ClaimOwner          *string  `json:"claim_owner"`
	Fence               int64    `json:"fence"`
	RetainUntilResolved bool     `json:"retain_until_resolved"`
}

type SnapshotFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

func ContentDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func SnapshotFiles(files map[string][]byte) []SnapshotFile {
	records := make([]SnapshotFile, 0, len(files))
	for path, raw := range files {
		records = append(records, SnapshotFile{Path: path, Digest: ContentDigest(raw)})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	return records
}

type PublicationBinding struct {
	GroupID, Publisher, StateIncarnationID, MembershipRevision string
	ContentRef, BaseCommit, ScopeDigest, ContractDigest        string
	SourceRevision                                             int64
	ReceiptIDs                                                 []string
}

func NewPublication(binding PublicationBinding, files []SnapshotFile) (Publication, error) {
	files = append([]SnapshotFile(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	snapshot, err := SnapshotDigest(files)
	if err != nil {
		return Publication{}, err
	}
	receipts := append([]string(nil), binding.ReceiptIDs...)
	sort.Strings(receipts)
	cause, err := framedDigest("agent-dispatch.sync-publication-cause/v1", struct {
		Group    string   `json:"group_id"`
		Source   int64    `json:"source_revision"`
		Receipts []string `json:"receipt_ids"`
	}{binding.GroupID, binding.SourceRevision, receipts})
	if err != nil {
		return Publication{}, err
	}
	idDigest, err := framedDigest("agent-dispatch.sync-publication-id/v1", struct {
		Group, Publisher, Incarnation, Membership, Base, Snapshot, Scope, Contract, Cause string
	}{binding.GroupID, binding.Publisher, binding.StateIncarnationID, binding.MembershipRevision, binding.BaseCommit, snapshot, binding.ScopeDigest, binding.ContractDigest, cause})
	if err != nil {
		return Publication{}, err
	}
	p := Publication{
		SchemaVersion: PublicationSchema, PublicationID: "publication-" + idDigest[len("sha256:"):len("sha256:")+32],
		GroupID: binding.GroupID, Publisher: binding.Publisher, StateIncarnationID: binding.StateIncarnationID,
		MembershipRevision: binding.MembershipRevision, ContentRef: binding.ContentRef, SourceRevision: binding.SourceRevision,
		BaseCommit: binding.BaseCommit, SnapshotDigest: snapshot, ScopeDigest: binding.ScopeDigest, ContractDigest: binding.ContractDigest,
		ReceiptIDs: receipts, CauseID: "cause-" + cause[len("sha256:"):len("sha256:")+32], State: "prepared", Reason: "none",
		Attempts: 0, Fence: 1, RetainUntilResolved: true,
	}
	if err := p.Validate(); err != nil {
		return Publication{}, err
	}
	return p, nil
}

func SnapshotDigest(files []SnapshotFile) (string, error) {
	ordered := append([]SnapshotFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	for i, file := range ordered {
		if file.Path == "" || !digestPattern.MatchString(file.Digest) || (i > 0 && ordered[i-1].Path >= file.Path) {
			return "", fmt.Errorf("%w: invalid snapshot file set", ErrInvalidRecord)
		}
	}
	return framedDigest("agent-dispatch.sync-snapshot/v1", ordered)
}

func (p Publication) Validate() error {
	if p.SchemaVersion != PublicationSchema || !identityPattern.MatchString(p.GroupID) || !identityPattern.MatchString(p.Publisher) ||
		!incarnationPattern.MatchString(p.StateIncarnationID) || !oidPattern.MatchString(p.MembershipRevision) || !validContentRef(p.ContentRef) ||
		p.SourceRevision < 1 || !oidPattern.MatchString(p.BaseCommit) || !digestPattern.MatchString(p.SnapshotDigest) ||
		!digestPattern.MatchString(p.ScopeDigest) || !digestPattern.MatchString(p.ContractDigest) || len(p.ReceiptIDs) == 0 || len(p.ReceiptIDs) > 100 ||
		!recordID(p.PublicationID) || !recordID(p.CauseID) || p.State != "prepared" || p.Reason != "none" || p.Attempts != 0 || p.ClaimOwner != nil || p.Fence != 1 || !p.RetainUntilResolved {
		return fmt.Errorf("%w: invalid prepared publication", ErrInvalidRecord)
	}
	seen := map[string]bool{}
	for i, id := range p.ReceiptIDs {
		if !recordID(id) || seen[id] || (i > 0 && p.ReceiptIDs[i-1] >= id) {
			return fmt.Errorf("%w: invalid receipt identity set", ErrInvalidRecord)
		}
		seen[id] = true
	}
	return nil
}

func CanonicalPublication(p Publication) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}
func DecodePublication(raw []byte) (Publication, error) {
	var p Publication
	if err := decodeStrict(raw, &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

type Checkpoint struct {
	SchemaVersion      string `json:"schema_version"`
	CheckpointID       string `json:"checkpoint_id"`
	GroupID            string `json:"group_id"`
	Kind               string `json:"kind"`
	TargetCommit       string `json:"target_commit"`
	SnapshotDigest     string `json:"snapshot_digest"`
	ScopeDigest        string `json:"scope_digest"`
	ContractDigest     string `json:"contract_digest"`
	MembershipRevision string `json:"membership_revision"`
	AdministratorKey   string `json:"administrator_key"`
}
type CheckpointPlan struct {
	SchemaVersion              string     `json:"schema_version"`
	PlanID                     string     `json:"plan_id"`
	GroupID                    string     `json:"group_id"`
	ExpectedContentPredecessor string     `json:"expected_content_predecessor"`
	ExpectedMembershipRevision string     `json:"expected_membership_revision"`
	ProposedCheckpoint         Checkpoint `json:"proposed_checkpoint"`
	AdministratorKey           string     `json:"administrator_key"`
}

func NewCheckpointPlan(group, kind, target, snapshot, scope, contract, membership, predecessor, admin string) (CheckpointPlan, error) {
	cp := Checkpoint{SchemaVersion: CheckpointSchema, GroupID: group, Kind: kind, TargetCommit: target, SnapshotDigest: snapshot, ScopeDigest: scope, ContractDigest: contract, MembershipRevision: membership, AdministratorKey: admin}
	d, err := framedDigest("agent-dispatch.sync-checkpoint-id/v1", cp)
	if err != nil {
		return CheckpointPlan{}, err
	}
	cp.CheckpointID = "checkpoint-" + d[len("sha256:"):len("sha256:")+32]
	pl := CheckpointPlan{SchemaVersion: CheckpointPlanSchema, GroupID: group, ExpectedContentPredecessor: predecessor, ExpectedMembershipRevision: membership, ProposedCheckpoint: cp, AdministratorKey: admin}
	d, err = framedDigest("agent-dispatch.sync-checkpoint-plan-id/v1", pl)
	if err != nil {
		return CheckpointPlan{}, err
	}
	pl.PlanID = "checkpoint-plan-" + d[len("sha256:"):len("sha256:")+32]
	return pl, pl.Validate()
}
func (c Checkpoint) Validate() error {
	if c.SchemaVersion != CheckpointSchema || !recordID(c.CheckpointID) || !identityPattern.MatchString(c.GroupID) || !checkpointKind(c.Kind) || !oidPattern.MatchString(c.TargetCommit) || !digestPattern.MatchString(c.SnapshotDigest) || !digestPattern.MatchString(c.ScopeDigest) || !digestPattern.MatchString(c.ContractDigest) || !oidPattern.MatchString(c.MembershipRevision) || !fingerprintPattern.MatchString(c.AdministratorKey) {
		return fmt.Errorf("%w: invalid checkpoint", ErrInvalidRecord)
	}
	copy := c
	copy.CheckpointID = ""
	d, _ := framedDigest("agent-dispatch.sync-checkpoint-id/v1", copy)
	if c.CheckpointID != "checkpoint-"+d[len("sha256:"):len("sha256:")+32] {
		return fmt.Errorf("%w: checkpoint_id mismatch", ErrInvalidRecord)
	}
	return nil
}
func (p CheckpointPlan) Validate() error {
	if p.SchemaVersion != CheckpointPlanSchema || !recordID(p.PlanID) || p.GroupID != p.ProposedCheckpoint.GroupID || p.ExpectedMembershipRevision != p.ProposedCheckpoint.MembershipRevision || p.AdministratorKey != p.ProposedCheckpoint.AdministratorKey || !oidPattern.MatchString(p.ExpectedContentPredecessor) || !oidPattern.MatchString(p.ExpectedMembershipRevision) {
		return fmt.Errorf("%w: invalid checkpoint plan binding", ErrInvalidRecord)
	}
	if err := p.ProposedCheckpoint.Validate(); err != nil {
		return err
	}
	copy := p
	copy.PlanID = ""
	d, _ := framedDigest("agent-dispatch.sync-checkpoint-plan-id/v1", copy)
	if p.PlanID != "checkpoint-plan-"+d[len("sha256:"):len("sha256:")+32] {
		return fmt.Errorf("%w: checkpoint plan_id mismatch", ErrInvalidRecord)
	}
	return nil
}
func CanonicalCheckpoint(c Checkpoint) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}
func DecodeCheckpoint(raw []byte) (Checkpoint, error) {
	var c Checkpoint
	if err := decodeStrict(raw, &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func CanonicalCheckpointPlan(p CheckpointPlan) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}
func DecodeCheckpointPlan(raw []byte) (CheckpointPlan, error) {
	var p CheckpointPlan
	if err := decodeStrict(raw, &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

type Nudge struct {
	SchemaVersion      string `json:"schema_version"`
	GroupID            string `json:"group_id"`
	PublicationID      string `json:"publication_id"`
	Sender             string `json:"sender"`
	Receiver           string `json:"receiver"`
	MembershipRevision string `json:"membership_revision"`
	ContentRef         string `json:"content_ref"`
	TargetCommit       string `json:"target_commit"`
}

func (n Nudge) Validate() error {
	if n.SchemaVersion != NudgeSchema || !identityPattern.MatchString(n.GroupID) || !recordID(n.PublicationID) || !identityPattern.MatchString(n.Sender) || !identityPattern.MatchString(n.Receiver) || n.Sender == n.Receiver || !oidPattern.MatchString(n.MembershipRevision) || !validContentRef(n.ContentRef) || !oidPattern.MatchString(n.TargetCommit) {
		return fmt.Errorf("%w: invalid sync nudge", ErrInvalidRecord)
	}
	return nil
}

func CanonicalNudge(n Nudge) ([]byte, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(n)
}

func recordID(v string) bool { return len(v) > 0 && len(v) <= 128 && regexpRecordID(v) }
func regexpRecordID(v string) bool {
	for i, b := range []byte(v) {
		if (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || (b == '-' && i > 0) {
			continue
		}
		return false
	}
	return true
}
func checkpointKind(v string) bool {
	return v == "initial_baseline" || v == "conflict_resolution" || v == "history_bound_exhausted"
}
func framedDigest(domain string, v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(raw)
	h.Write([]byte{'\n'})
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

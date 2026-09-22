package syncrecords

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

const ImportSchema = "agent-dispatch.sync-import/v1"

// ImportPath is one exact live-tree effect. "absent" is deliberately a
// first-class value so deletions have the same attribution strength as writes.
type ImportPath struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// Import is the immutable pre-apply record. The sync job head and append-only
// journal carry later state changes; this document is never rewritten after an
// external effect starts.
type Import struct {
	SchemaVersion               string       `json:"schema_version"`
	ImportID                    string       `json:"import_id"`
	PlanID                      string       `json:"plan_id"`
	JournalID                   string       `json:"journal_id"`
	GroupID                     string       `json:"group_id"`
	FromCommit                  string       `json:"from_commit"`
	TargetCommit                string       `json:"target_commit"`
	MembershipRevision          string       `json:"membership_revision"`
	AcknowledgementID           string       `json:"acknowledgement_id"`
	ResourceObservationRevision int64        `json:"resource_observation_revision"`
	ExpectedGitStateDigest      string       `json:"expected_git_state_digest"`
	HistoryEvidenceID           string       `json:"history_evidence_id"`
	CaseMode                    string       `json:"case_mode"`
	ControllerOnly              bool         `json:"controller_only,omitempty"`
	State                       string       `json:"state"`
	Reason                      string       `json:"reason"`
	Paths                       []ImportPath `json:"paths"`
	Attempts                    int          `json:"attempts"`
	ClaimOwner                  *string      `json:"claim_owner"`
	Fence                       int64        `json:"fence"`
	RetainUntilResolved         bool         `json:"retain_until_resolved"`
}

type ImportBinding struct {
	GroupID, FromCommit, TargetCommit, MembershipRevision        string
	AcknowledgementID, ExpectedGitStateDigest, HistoryEvidenceID string
	CaseMode, State, Reason                                      string
	ResourceObservationRevision                                  int64
	ControllerOnly                                               bool
}

func NewImport(binding ImportBinding, paths []ImportPath) (Import, error) {
	// Keep an empty effect set as [] rather than null. The published schema
	// requires controller-only imports to carry an explicit empty array.
	ordered := append([]ImportPath{}, paths...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	p := Import{
		SchemaVersion: ImportSchema, GroupID: binding.GroupID,
		FromCommit: binding.FromCommit, TargetCommit: binding.TargetCommit,
		MembershipRevision: binding.MembershipRevision, AcknowledgementID: binding.AcknowledgementID,
		ResourceObservationRevision: binding.ResourceObservationRevision,
		ExpectedGitStateDigest:      binding.ExpectedGitStateDigest, HistoryEvidenceID: binding.HistoryEvidenceID,
		CaseMode: binding.CaseMode, ControllerOnly: binding.ControllerOnly, State: binding.State, Reason: binding.Reason, Paths: ordered,
		Attempts: 0, Fence: 1, RetainUntilResolved: binding.State != "applied",
	}
	planProjection := p
	planProjection.ImportID, planProjection.PlanID, planProjection.JournalID = "", "", ""
	d, err := framedDigest("agent-dispatch.sync-import-plan-id/v1", planProjection)
	if err != nil {
		return Import{}, err
	}
	p.PlanID = "import-plan-" + d[len("sha256:"):len("sha256:")+32]
	p.JournalID = "import-journal-" + d[len("sha256:"):len("sha256:")+32]
	idProjection := p
	idProjection.ImportID = ""
	d, err = framedDigest("agent-dispatch.sync-import-id/v1", idProjection)
	if err != nil {
		return Import{}, err
	}
	p.ImportID = "import-" + d[len("sha256:"):len("sha256:")+32]
	return p, p.Validate()
}

func (p Import) Validate() error {
	if p.SchemaVersion != ImportSchema || !recordID(p.ImportID) || !recordID(p.PlanID) || !recordID(p.JournalID) ||
		!identityPattern.MatchString(p.GroupID) || !oidPattern.MatchString(p.FromCommit) || !oidPattern.MatchString(p.TargetCommit) ||
		p.FromCommit == p.TargetCommit || !oidPattern.MatchString(p.MembershipRevision) || !recordID(p.AcknowledgementID) ||
		p.ResourceObservationRevision < 1 || !digestPattern.MatchString(p.ExpectedGitStateDigest) || !recordID(p.HistoryEvidenceID) ||
		(p.CaseMode != "sensitive" && p.CaseMode != "insensitive") || p.Attempts < 0 || p.Attempts > 20 || p.Fence < 1 ||
		len(p.Paths) > 1000 || (p.ControllerOnly && len(p.Paths) != 0) || (!p.ControllerOnly && len(p.Paths) == 0) {
		return fmt.Errorf("%w: invalid import binding", ErrInvalidRecord)
	}
	if !validImportDisposition(p.State, p.Reason, p.RetainUntilResolved) {
		return fmt.Errorf("%w: invalid import disposition", ErrInvalidRecord)
	}
	if p.ClaimOwner != nil && !recordID(*p.ClaimOwner) {
		return fmt.Errorf("%w: invalid import claim owner", ErrInvalidRecord)
	}
	aliases := map[string]string{}
	for i, effect := range p.Paths {
		normalized, err := records.NormalizePath(effect.Path)
		lower := strings.ToLower(effect.Path)
		if err != nil || normalized != effect.Path || (!strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".markdown")) ||
			!digestOrAbsent(effect.Before) || !digestOrAbsent(effect.After) || effect.Before == effect.After ||
			(i > 0 && p.Paths[i-1].Path >= effect.Path) {
			return fmt.Errorf("%w: invalid import path set", ErrInvalidRecord)
		}
		// Import identity is deliberately conservative on every host. The
		// recorded case mode describes the current filesystem; it never lets a
		// caller admit a tree that would alias on the peer or after relocation.
		key := records.PortablePathIdentity(effect.Path)
		if prior, exists := aliases[key]; exists && prior != effect.Path {
			return fmt.Errorf("%w: import paths alias", ErrInvalidRecord)
		}
		aliases[key] = effect.Path
	}
	copy := p
	copy.ImportID = ""
	d, _ := framedDigest("agent-dispatch.sync-import-id/v1", copy)
	if p.ImportID != "import-"+d[len("sha256:"):len("sha256:")+32] {
		return fmt.Errorf("%w: import_id mismatch", ErrInvalidRecord)
	}
	copy = p
	copy.ImportID, copy.PlanID, copy.JournalID = "", "", ""
	d, _ = framedDigest("agent-dispatch.sync-import-plan-id/v1", copy)
	if p.PlanID != "import-plan-"+d[len("sha256:"):len("sha256:")+32] {
		return fmt.Errorf("%w: plan_id mismatch", ErrInvalidRecord)
	}
	return nil
}

func validImportDisposition(state, reason string, retained bool) bool {
	switch state {
	case "requested", "fetched", "validated", "applying":
		return reason == "none" && retained
	case "applied":
		return reason == "none" && !retained
	case "deferred":
		return retained && (reason == "acknowledgement_stale" || reason == "local_overlap" || reason == "untracked_collision" || reason == "git_unstable" || reason == "observation_unavailable" || reason == "resource_busy")
	case "blocked":
		return retained && (reason == "membership_stale" || reason == "history_uncovered" || reason == "history_diverged" || reason == "trust_failed" || reason == "bound_exhausted")
	case "recovering", "uncertain":
		return retained && reason == "partial_effect"
	default:
		return false
	}
}

func digestOrAbsent(value string) bool { return value == "absent" || digestPattern.MatchString(value) }

func CanonicalImport(p Import) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

func DecodeImport(raw []byte) (Import, error) {
	var p Import
	if err := decodeStrict(raw, &p); err != nil {
		return p, err
	}
	return p, p.Validate()
}

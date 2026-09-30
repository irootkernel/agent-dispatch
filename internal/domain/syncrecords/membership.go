// Package syncrecords owns the closed, signed Wiki sync record vocabulary.
package syncrecords

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MembershipSchema = "agent-dispatch.sync-membership/v1"
	PlanSchema       = "agent-dispatch.sync-membership-plan/v1"
	MaxRecordBytes   = 256 << 10
)

var (
	ErrInvalidRecord       = errors.New("invalid sync membership record")
	ErrInvalidTransition   = errors.New("invalid sync membership transition")
	ErrRemovedKeyFirstSeen = errors.New("removed publisher key cannot authorize first-seen evidence")
	identityPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	incarnationPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,127}$`)
	endpointHostPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?\.ts\.net$`)
	oidPattern             = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	digestPattern          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	fingerprintPattern     = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{42}[AEIMQUYcgkosw048]$`)
)

type ActiveMember struct {
	InstanceID         string `json:"instance_id"`
	StateIncarnationID string `json:"state_incarnation_id"`
	PublisherKey       string `json:"publisher_key"`
	Endpoint           string `json:"endpoint"`
}

type HistoricalMember struct {
	InstanceID         string `json:"instance_id"`
	StateIncarnationID string `json:"state_incarnation_id"`
	State              string `json:"state"`
	PublisherKey       string `json:"publisher_key"`
	Reason             string `json:"reason"`
}

type Membership struct {
	SchemaVersion     string             `json:"schema_version"`
	GroupID           string             `json:"group_id"`
	Predecessor       *string            `json:"predecessor"`
	ContentRef        string             `json:"content_ref"`
	ContentBinding    string             `json:"content_binding"`
	ContractDigest    string             `json:"contract_digest"`
	Mode              string             `json:"mode"`
	BlockReason       string             `json:"block_reason,omitempty"`
	AdministratorKey  string             `json:"administrator_key"`
	ActiveMembers     []ActiveMember     `json:"active_members"`
	HistoricalMembers []HistoricalMember `json:"historical_members"`
}

type MembershipPlan struct {
	SchemaVersion       string     `json:"schema_version"`
	PlanID              string     `json:"plan_id"`
	GroupID             string     `json:"group_id"`
	ChangeKind          string     `json:"change_kind"`
	TargetInstance      string     `json:"target_instance,omitempty"`
	ExpectedPredecessor *string    `json:"expected_predecessor"`
	ProposedMembership  Membership `json:"proposed_membership"`
	AdministratorKey    string     `json:"administrator_key"`
}

type Binding struct {
	GroupID          string
	ContentRef       string
	ContentBinding   string
	ContractDigest   string
	AdministratorKey string
}

func DecodeMembership(raw []byte) (Membership, error) {
	var out Membership
	if err := decodeStrict(raw, &out); err != nil {
		return out, err
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func DecodePlan(raw []byte) (MembershipPlan, error) {
	var out MembershipPlan
	if err := decodeStrict(raw, &out); err != nil {
		return out, err
	}
	// Required nullable fields must be present even when bootstrap names no predecessor.
	var fields struct {
		ExpectedPredecessor json.RawMessage `json:"expected_predecessor"`
		ProposedMembership  struct {
			Predecessor json.RawMessage `json:"predecessor"`
		} `json:"proposed_membership"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return out, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	if len(fields.ExpectedPredecessor) == 0 {
		return out, fmt.Errorf("%w: expected_predecessor is required", ErrInvalidRecord)
	}
	if len(fields.ProposedMembership.Predecessor) == 0 {
		return out, fmt.Errorf("%w: proposed_membership.predecessor is required", ErrInvalidRecord)
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

func decodeStrict(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > MaxRecordBytes || !utf8.Valid(raw) {
		return fmt.Errorf("%w: JSON is empty, oversized, or invalid UTF-8", ErrInvalidRecord)
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRecord, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: JSON must contain one value", ErrInvalidRecord)
	}
	return nil
}

func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate or non-string object key %q", key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err := dec.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("JSON must contain one value")
	}
	return nil
}

func (m Membership) Validate() error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidRecord, fmt.Sprintf(format, args...))
	}
	if m.SchemaVersion != MembershipSchema || !identityPattern.MatchString(m.GroupID) || !validContentRef(m.ContentRef) || !digestPattern.MatchString(m.ContentBinding) || !digestPattern.MatchString(m.ContractDigest) || !fingerprintPattern.MatchString(m.AdministratorKey) {
		return bad("invalid membership binding")
	}
	if m.Predecessor != nil && !oidPattern.MatchString(*m.Predecessor) {
		return bad("invalid predecessor")
	}
	if len(m.HistoricalMembers) > 1000 {
		return bad("historical membership exceeds 1000 entries")
	}
	switch m.Mode {
	case "normal":
		if len(m.ActiveMembers) != 2 || m.BlockReason != "" {
			return bad("normal membership requires exactly two active members and no block reason")
		}
	case "blocked_emergency":
		if len(m.ActiveMembers) > 1 || (m.BlockReason != "emergency_revocation" && m.BlockReason != "active_member_shortfall") {
			return bad("blocked emergency membership requires at most one active member and a closed block reason")
		}
	default:
		return bad("invalid membership mode")
	}
	instances, incarnations, activeKeys := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, member := range m.ActiveMembers {
		if !validActive(member) || instances[member.InstanceID] || incarnations[member.StateIncarnationID] || activeKeys[member.PublisherKey] || member.PublisherKey == m.AdministratorKey {
			return bad("active member identities, incarnations, and key roles must be valid and distinct")
		}
		instances[member.InstanceID], incarnations[member.StateIncarnationID], activeKeys[member.PublisherKey] = true, true, true
	}
	historyIdentity := map[string]bool{}
	for _, member := range m.HistoricalMembers {
		key := member.InstanceID + "\x00" + member.StateIncarnationID + "\x00" + member.PublisherKey
		if !validHistorical(member) || incarnations[member.StateIncarnationID] || historyIdentity[key] || member.PublisherKey == m.AdministratorKey {
			return bad("historical member identities, incarnations, and key roles must be valid and unique")
		}
		incarnations[member.StateIncarnationID], historyIdentity[key] = true, true
	}
	return nil
}

func (p MembershipPlan) Validate() error {
	if p.SchemaVersion != PlanSchema || !identityPattern.MatchString(p.GroupID) || p.GroupID != p.ProposedMembership.GroupID || p.AdministratorKey != p.ProposedMembership.AdministratorKey || !fingerprintPattern.MatchString(p.AdministratorKey) {
		return fmt.Errorf("%w: plan binding mismatch", ErrInvalidRecord)
	}
	if !validChange(p.ChangeKind) || (p.TargetInstance != "" && !identityPattern.MatchString(p.TargetInstance)) {
		return fmt.Errorf("%w: invalid change kind or target instance", ErrInvalidRecord)
	}
	if p.ChangeKind == "bootstrap" {
		if p.ExpectedPredecessor != nil || p.ProposedMembership.Predecessor != nil || p.TargetInstance != "" {
			return fmt.Errorf("%w: bootstrap must not name a predecessor or target", ErrInvalidRecord)
		}
	} else if p.TargetInstance == "" || p.ExpectedPredecessor == nil || p.ProposedMembership.Predecessor == nil || *p.ExpectedPredecessor != *p.ProposedMembership.Predecessor || !oidPattern.MatchString(*p.ExpectedPredecessor) {
		return fmt.Errorf("%w: update requires one target and exact predecessor", ErrInvalidRecord)
	}
	if err := p.ProposedMembership.Validate(); err != nil {
		return err
	}
	want, err := planID(p)
	if err != nil || p.PlanID != want {
		return fmt.Errorf("%w: plan_id does not match canonical plan", ErrInvalidRecord)
	}
	return nil
}

func CanonicalMembership(m Membership) ([]byte, error) {
	normalizeMembership(&m)
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

func CanonicalPlan(p MembershipPlan) ([]byte, error) {
	normalizeMembership(&p.ProposedMembership)
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

func NewPlan(kind, target string, current *Membership, predecessor *string, desired []ActiveMember, binding Binding) (MembershipPlan, error) {
	proposed := Membership{
		SchemaVersion: MembershipSchema, GroupID: binding.GroupID, Predecessor: cloneString(predecessor),
		ContentRef: binding.ContentRef, ContentBinding: binding.ContentBinding, ContractDigest: binding.ContractDigest,
		Mode: "normal", AdministratorKey: binding.AdministratorKey,
		ActiveMembers: append([]ActiveMember(nil), desired...), HistoricalMembers: []HistoricalMember{},
	}
	if current != nil {
		proposed.HistoricalMembers = append(proposed.HistoricalMembers, current.HistoricalMembers...)
	}
	if err := applyChange(kind, target, current, &proposed); err != nil {
		return MembershipPlan{}, err
	}
	normalizeMembership(&proposed)
	plan := MembershipPlan{SchemaVersion: PlanSchema, GroupID: binding.GroupID, ChangeKind: kind, TargetInstance: target, ExpectedPredecessor: cloneString(predecessor), ProposedMembership: proposed, AdministratorKey: binding.AdministratorKey}
	id, err := planID(plan)
	if err != nil {
		return MembershipPlan{}, err
	}
	plan.PlanID = id
	if err := plan.Validate(); err != nil {
		return MembershipPlan{}, err
	}
	return plan, nil
}

func RebuildPlan(plan MembershipPlan, current *Membership, desired []ActiveMember, binding Binding) (MembershipPlan, error) {
	return NewPlan(plan.ChangeKind, plan.TargetInstance, current, plan.ExpectedPredecessor, desired, binding)
}

func applyChange(kind, target string, current *Membership, proposed *Membership) error {
	fail := func(s string) error { return fmt.Errorf("%w: %s", ErrInvalidTransition, s) }
	if kind == "bootstrap" {
		if current != nil || target != "" || len(proposed.ActiveMembers) != 2 {
			return fail("bootstrap requires an absent history and two configured members")
		}
		return proposed.Validate()
	}
	if current == nil || proposed.Predecessor == nil {
		return fail("a non-bootstrap change requires current membership")
	}
	if target == "" {
		return fail("a non-bootstrap change requires --instance")
	}
	old, oldOK := activeByID(current.ActiveMembers, target)
	switch kind {
	case "retirement", "emergency_revocation":
		if !oldOK {
			return fail("target instance is not active")
		}
		proposed.ActiveMembers = removeActive(current.ActiveMembers, target)
		proposed.Mode = "blocked_emergency"
		state, reason, block := "retired", "planned_replacement", "active_member_shortfall"
		if kind == "emergency_revocation" {
			state, reason, block = "revoked", "credential_compromise", "emergency_revocation"
		}
		proposed.BlockReason = block
		proposed.HistoricalMembers = append(proposed.HistoricalMembers, historical(old, state, reason))
	case "endpoint_update":
		if !oldOK || len(current.ActiveMembers) != 2 || !sameExcept(current.ActiveMembers, proposed.ActiveMembers, target, "endpoint") {
			return fail("endpoint update must change only the target endpoint")
		}
	case "key_rotation":
		if !oldOK || len(current.ActiveMembers) != 2 || !sameExcept(current.ActiveMembers, proposed.ActiveMembers, target, "key") {
			return fail("key rotation must change only target publisher key and incarnation")
		}
		proposed.HistoricalMembers = append(proposed.HistoricalMembers, historical(old, "retired", "key_rotation"))
	case "incarnation_registration":
		if !oldOK || len(current.ActiveMembers) != 2 || !sameExcept(current.ActiveMembers, proposed.ActiveMembers, target, "incarnation") {
			return fail("incarnation registration must change only the target incarnation")
		}
		proposed.HistoricalMembers = append(proposed.HistoricalMembers, historical(old, "retired", "incarnation_rotation"))
	case "replacement":
		if current.Mode == "normal" {
			if !oldOK || !validReplacement(current.ActiveMembers, proposed.ActiveMembers, target) {
				return fail("replacement must remove the target and add exactly one configured member")
			}
			proposed.HistoricalMembers = append(proposed.HistoricalMembers, historical(old, "retired", "planned_replacement"))
		} else if current.Mode == "blocked_emergency" {
			if oldOK || !validRecovery(current.ActiveMembers, proposed.ActiveMembers, target) {
				return fail("blocked recovery must add the target beside the retained member")
			}
		} else {
			return fail("unsupported current membership mode")
		}
	default:
		return fail("unsupported membership change")
	}
	return proposed.Validate()
}

func ValidateTransition(current Membership, plan MembershipPlan) error {
	rebuilt, err := NewPlan(plan.ChangeKind, plan.TargetInstance, &current, plan.ExpectedPredecessor, plan.ProposedMembership.ActiveMembers, Binding{
		GroupID: current.GroupID, ContentRef: current.ContentRef, ContentBinding: current.ContentBinding,
		ContractDigest: current.ContractDigest, AdministratorKey: current.AdministratorKey,
	})
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(rebuilt, plan) {
		return fmt.Errorf("%w: plan is not the unique transition from its predecessor", ErrInvalidTransition)
	}
	return nil
}

func AuthorizePublisher(atRevision, currentRevision Membership, key string, previouslyVerified, administratorCheckpoint bool) error {
	if !activeKey(atRevision, key) {
		return fmt.Errorf("%w: publisher key was not active at the named revision", ErrInvalidTransition)
	}
	if activeKey(currentRevision, key) || previouslyVerified || administratorCheckpoint {
		return nil
	}
	return ErrRemovedKeyFirstSeen
}

func planID(p MembershipPlan) (string, error) {
	p.PlanID = ""
	normalizeMembership(&p.ProposedMembership)
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("agent-dispatch.sync-membership-plan-id/v1\x00"))
	_, _ = h.Write(raw)
	_, _ = h.Write([]byte{'\n'})
	return "membership-" + hex.EncodeToString(h.Sum(nil)), nil
}

func normalizeMembership(m *Membership) {
	if m.ActiveMembers == nil {
		m.ActiveMembers = []ActiveMember{}
	}
	if m.HistoricalMembers == nil {
		m.HistoricalMembers = []HistoricalMember{}
	}
	sort.Slice(m.ActiveMembers, func(i, j int) bool { return m.ActiveMembers[i].InstanceID < m.ActiveMembers[j].InstanceID })
	sort.SliceStable(m.HistoricalMembers, func(i, j int) bool {
		if m.HistoricalMembers[i].InstanceID != m.HistoricalMembers[j].InstanceID {
			return m.HistoricalMembers[i].InstanceID < m.HistoricalMembers[j].InstanceID
		}
		return m.HistoricalMembers[i].StateIncarnationID < m.HistoricalMembers[j].StateIncarnationID
	})
}

func validActive(m ActiveMember) bool {
	if !identityPattern.MatchString(m.InstanceID) || !incarnationPattern.MatchString(m.StateIncarnationID) || !fingerprintPattern.MatchString(m.PublisherKey) {
		return false
	}
	_, err := ParseTailnetEndpoint(m.Endpoint)
	return err == nil
}

func validHistorical(m HistoricalMember) bool {
	if !identityPattern.MatchString(m.InstanceID) || !incarnationPattern.MatchString(m.StateIncarnationID) || !fingerprintPattern.MatchString(m.PublisherKey) {
		return false
	}
	if m.State != "retired" && m.State != "revoked" {
		return false
	}
	switch m.Reason {
	case "planned_replacement", "incarnation_rotation", "key_rotation", "credential_compromise", "operator_revocation":
		return true
	}
	return false
}

func validContentRef(ref string) bool {
	return strings.HasPrefix(ref, "refs/heads/") && len(ref) > len("refs/heads/") && !strings.Contains(ref, "..") && !strings.ContainsAny(ref, " ~^:?*[\\") && !strings.HasSuffix(ref, "/")
}

func validChange(kind string) bool {
	switch kind {
	case "bootstrap", "endpoint_update", "key_rotation", "replacement", "retirement", "emergency_revocation", "incarnation_registration":
		return true
	}
	return false
}

func activeByID(members []ActiveMember, id string) (ActiveMember, bool) {
	for _, member := range members {
		if member.InstanceID == id {
			return member, true
		}
	}
	return ActiveMember{}, false
}

func activeKey(m Membership, key string) bool {
	for _, member := range m.ActiveMembers {
		if member.PublisherKey == key {
			return true
		}
	}
	return false
}

func removeActive(members []ActiveMember, id string) []ActiveMember {
	out := make([]ActiveMember, 0, len(members)-1)
	for _, member := range members {
		if member.InstanceID != id {
			out = append(out, member)
		}
	}
	return out
}

func historical(m ActiveMember, state, reason string) HistoricalMember {
	return HistoricalMember{InstanceID: m.InstanceID, StateIncarnationID: m.StateIncarnationID, State: state, PublisherKey: m.PublisherKey, Reason: reason}
}

func sameExcept(old, desired []ActiveMember, target, allowed string) bool {
	if len(old) != len(desired) {
		return false
	}
	changed := false
	for _, before := range old {
		after, ok := activeByID(desired, before.InstanceID)
		if !ok {
			return false
		}
		if before.InstanceID != target {
			if before != after {
				return false
			}
			continue
		}
		switch allowed {
		case "endpoint":
			if before.Endpoint == after.Endpoint || before.StateIncarnationID != after.StateIncarnationID || before.PublisherKey != after.PublisherKey {
				return false
			}
		case "key":
			if before.PublisherKey == after.PublisherKey || before.StateIncarnationID == after.StateIncarnationID || before.Endpoint != after.Endpoint {
				return false
			}
		case "incarnation":
			if before.StateIncarnationID == after.StateIncarnationID || before.PublisherKey != after.PublisherKey || before.Endpoint != after.Endpoint {
				return false
			}
		}
		changed = true
	}
	return changed
}

func validReplacement(old, desired []ActiveMember, target string) bool {
	if len(old) != 2 || len(desired) != 2 {
		return false
	}
	retained := 0
	for _, member := range old {
		if member.InstanceID == target {
			continue
		}
		after, ok := activeByID(desired, member.InstanceID)
		if !ok || after != member {
			return false
		}
		retained++
	}
	_, targetStillPresent := activeByID(desired, target)
	return retained == 1 && !targetStillPresent
}

func validRecovery(old, desired []ActiveMember, target string) bool {
	if len(old) > 1 || len(desired) != 2 {
		return false
	}
	added, ok := activeByID(desired, target)
	if !ok || !validActive(added) {
		return false
	}
	for _, retained := range old {
		after, ok := activeByID(desired, retained.InstanceID)
		if !ok || after != retained {
			return false
		}
	}
	return true
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

// Package syncmanagement defines the local management ledger's immutable inputs.
package syncmanagement

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	Schema                  = "agent-dispatch.sync-management/v1"
	DraftTTLSeconds         = 600
	ExpiredRetentionSeconds = 86400
	DataDraftLimit          = 256
	ControlDraftLimit       = 16
	DataQueueMin            = 2
	DataQueueMax            = 1000
	ControlQueueLimit       = 16
	DataTerminalLimit       = 10000
	ControlTerminalLimit    = 1000
	AssociationLimit        = 1000
	AttemptLimit            = 20
	LeaseSeconds            = 30
	ExecutionSeconds        = 240
	CallSeconds             = 5
	ReasonBytes             = 256
	MinimumRetentionDays    = 180
)

var (
	handlePattern = regexp.MustCompile(`^mgr_[0-9a-f]{32}$`)
	groupPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

func ValidHandle(v string) bool    { return handlePattern.MatchString(v) }
func ValidID(v string) bool        { return idPattern.MatchString(v) }
func ValidCommit(v string) bool    { return commitPattern.MatchString(v) }
func IsControl(action string) bool { return action == "pause" || action == "resume" }
func ValidAction(action string) bool {
	return action == "sync-now" || action == "verify-all" || action == "retry" || IsControl(action)
}

func NewHandle() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "mgr_" + hex.EncodeToString(b[:]), nil
}

// Binding is the exact frozen fingerprint projection. Nullable inputs remain
// explicit, including the predecessor and control revision.
type Binding struct {
	GroupID                 string  `json:"group_id"`
	InstanceID              string  `json:"instance_id"`
	StateIncarnationID      string  `json:"state_incarnation_id"`
	Action                  string  `json:"action"`
	TargetRequestID         *string `json:"target_request_id"`
	PolicyRevision          string  `json:"policy_revision"`
	ConfigRevision          string  `json:"config_revision"`
	ExpectedControlRevision *int64  `json:"expected_control_revision"`
}

func (b Binding) Validate() error {
	if !groupPattern.MatchString(b.GroupID) || !ValidID(b.InstanceID) || !ValidID(b.StateIncarnationID) || !ValidAction(b.Action) || !digestPattern.MatchString(b.PolicyRevision) || !digestPattern.MatchString(b.ConfigRevision) {
		return fmt.Errorf("invalid management binding")
	}
	if (b.Action == "retry") != (b.TargetRequestID != nil) || (b.TargetRequestID != nil && !ValidHandle(*b.TargetRequestID)) || IsControl(b.Action) != (b.ExpectedControlRevision != nil) || (b.ExpectedControlRevision != nil && *b.ExpectedControlRevision < 1) {
		return fmt.Errorf("invalid management action preconditions")
	}
	return nil
}

// Digest uses lexical object keys, exact integers, no HTML escaping and no
// final newline. Go's encoder retains the required U+2028/U+2029 escapes.
func Digest(domain string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var projection any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&projection); err != nil {
		return "", err
	}
	var out bytes.Buffer
	e := json.NewEncoder(&out)
	e.SetEscapeHTML(false)
	if err := e.Encode(projection); err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(bytes.TrimSuffix(out.Bytes(), []byte{'\n'}))
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (b Binding) Fingerprint() (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	return Digest("agent-dispatch.sync-management.request/v1", b)
}

// Outcome records an action result, independently from acceptance or lookup.
type Outcome struct {
	Status          string  `json:"status"`
	TargetCommit    *string `json:"target_commit"`
	VerificationID  *string `json:"verification_id"`
	ControlRevision *int64  `json:"control_revision"`
	Quiescence      *string `json:"quiescence"`
	Reason          string  `json:"reason"`
}

func (o Outcome) Validate(action string) error {
	if !ValidAction(action) || !utf8.ValidString(o.Reason) || len(o.Reason) > ReasonBytes || strings.ContainsRune(o.Reason, 0) {
		return fmt.Errorf("invalid management outcome")
	}
	switch o.Status {
	case "intent_committed", "complete", "incomplete", "target_changed", "expired", "already_settled", "no_op", "failed", "refused", "blocked", "uncertain":
	default:
		return fmt.Errorf("invalid management outcome status")
	}
	if o.TargetCommit != nil && !ValidCommit(*o.TargetCommit) || o.VerificationID != nil && !ValidID(*o.VerificationID) {
		return fmt.Errorf("invalid management result identity")
	}
	if IsControl(action) {
		if o.Status != "intent_committed" || o.ControlRevision == nil || *o.ControlRevision < 1 || o.Quiescence == nil || (*o.Quiescence != "observed" && *o.Quiescence != "not_observed" && *o.Quiescence != "unknown") || o.TargetCommit != nil || o.VerificationID != nil {
			return fmt.Errorf("invalid control outcome")
		}
	} else {
		if o.Status == "intent_committed" || o.ControlRevision != nil || o.Quiescence != nil {
			return fmt.Errorf("data outcome contains control result")
		}
		if action == "verify-all" && (o.Status == "complete" || o.Status == "incomplete" || o.Status == "target_changed") && (o.TargetCommit == nil || o.VerificationID == nil) {
			return fmt.Errorf("verification outcome lacks pinned identity")
		}
	}
	return nil
}

func CanTransition(from, to string) bool {
	switch from {
	case "prepared":
		return to == "expired" || to == "queued" || to == "finished"
	case "queued":
		return to == "running" || to == "blocked"
	case "running":
		return to == "queued" || to == "blocked" || to == "uncertain" || to == "finished"
	case "blocked":
		return to == "queued" || to == "uncertain" || to == "finished"
	case "uncertain":
		return to == "queued" || to == "blocked" || to == "finished"
	}
	return false
}

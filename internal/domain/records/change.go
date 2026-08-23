package records

import (
	"sort"
	"unicode/utf8"
)

// ChangeItem is one observed path change (domain-model ChangeItem,
// source-observation $defs.change). Ordinal is the position in the
// original source payload and the final ordering tiebreaker.
type ChangeItem struct {
	Path         string
	Ordinal      int
	Operation    Operation
	ExistsAfter  bool
	FileType     FileType
	BeforeDigest Digest // empty means absent
	AfterDigest  Digest // empty means absent
	DigestStatus DigestStatus
}

// Validate fails closed on invalid enum state or an invalid path.
func (c ChangeItem) Validate() error {
	if _, err := ParseOperation(string(c.Operation)); err != nil {
		return err
	}
	if _, err := ParseFileType(string(c.FileType)); err != nil {
		return err
	}
	if _, err := ParseDigestStatus(string(c.DigestStatus)); err != nil {
		return err
	}
	if _, err := NormalizePath(c.Path); err != nil {
		return err
	}
	if c.BeforeDigest != "" {
		if _, err := ParseDigest(string(c.BeforeDigest)); err != nil {
			return err
		}
	}
	if c.AfterDigest != "" {
		if _, err := ParseDigest(string(c.AfterDigest)); err != nil {
			return err
		}
	}
	return nil
}

// OperationRank orders delete before create before modify (domain-model
// canonical change ordering). The fingerprint package reuses it so the
// ranking has one definition.
func OperationRank(op Operation) int {
	switch op {
	case OpDelete:
		return 0
	case OpCreate:
		return 1
	default:
		return 2
	}
}

// SortChanges orders changes canonically in place: by normalized UTF-8
// relative path bytes, then operation precedence (delete, create, modify),
// then original ordinal as the stable tiebreaker.
func SortChanges(changes []ChangeItem) {
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		pa, pb := OperationRank(a.Operation), OperationRank(b.Operation)
		if pa != pb {
			return pa < pb
		}
		return a.Ordinal < b.Ordinal
	})
}

// SourceFlags are the observation flags that can affect semantics and
// therefore enter the content fingerprint.
type SourceFlags struct {
	Overflow      bool `json:"overflow"`
	FreshInstance bool `json:"fresh_instance"`
	// RelativeRoot is the nullable relative-root flag text; empty encodes
	// null.
	RelativeRoot string `json:"relative_root"`
	// HasRelative has no published schema key: source.flags carries
	// only overflow, fresh_instance, and relative_root (E7-T8 round-1).
	HasRelative bool `json:"-"`
}

// SourceObservation is the immutable observation record (domain-model;
// DAT-007 keeps the causal and attribution fields).
type SourceObservation struct {
	SchemaVersion    string
	ObservationID    string
	SourceType       string
	SourceID         string
	SourceEventKey   string // empty encodes null
	TriggerName      string
	ResourceID       string
	ObservedAt       string
	ReceivedAt       string
	RawPayloadDigest Digest
	IngestStatus     string
	Flags            SourceFlags
	Changes          []ChangeItem
}

// Validate fails closed on structurally invalid content.
func (o *SourceObservation) Validate() error {
	if o.ObservationID == "" || len(o.ObservationID) < 16 {
		return errf("observation_id must be at least 16 characters")
	}
	if o.ResourceID == "" {
		return errf("resource_id is required")
	}
	if !utf8.ValidString(o.ObservationID) || !utf8.ValidString(o.ResourceID) {
		return errf("identity fields must be valid UTF-8")
	}
	if o.RawPayloadDigest != "" {
		if _, err := ParseDigest(string(o.RawPayloadDigest)); err != nil {
			return err
		}
	}
	for i := range o.Changes {
		if err := o.Changes[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// RouteRef identifies a route and the revision that made a decision.
type RouteRef struct {
	ID       string
	Revision string
}

// ContentFingerprintInput is the dedicated fingerprint projection
// (canonical-record-contracts §7: build a value object of only documented
// fields; never marshal arbitrary domain structs). Field declaration
// order is the lexicographic order of the JSON keys so the canonical
// encoding matches RFC 8785 key ordering.
type ContentFingerprintInput struct {
	Changes     []FingerprintChange `json:"changes"`
	ResourceID  string              `json:"resource_id"`
	Fresh       bool                `json:"source_flags_fresh_instance"`
	HasRelative bool                `json:"source_flags_has_relative_root"`
	Overflow    bool                `json:"source_flags_overflow"`
	// RelativeRoot carries the observation's relative-root flag text when
	// present; HasRelative distinguishes an empty value from an absent
	// one (domain-model source_flags_affecting_semantics).
	RelativeRoot string `json:"source_flags_relative_root"`
}

// FingerprintChange is the per-change fingerprint projection with keys in
// RFC 8785 lexicographic order.
type FingerprintChange struct {
	AfterDigest  string `json:"after_digest"`
	BeforeDigest string `json:"before_digest"`
	ExistsAfter  bool   `json:"exists_after"`
	Operation    string `json:"operation"`
	Path         string `json:"path"`
}

// IdempotencyKeyInput is the dedicated idempotency-key projection
// (domain-model §14) with keys in RFC 8785 lexicographic order. Attempt
// numbers, submission times, and other retry-varying fields are
// deliberately absent (DAT-006).
type IdempotencyKeyInput struct {
	ContentFingerprint string `json:"content_fingerprint"`
	Generation         int64  `json:"generation"`
	RequestVersion     string `json:"request_contract_version"`
	RouteID            string `json:"route_id"`
	RouteRevision      string `json:"route_revision"`
	TargetID           string `json:"target_id"`
}

func errf(msg string) error { return &ValidationError{msg} }

// ValidationError is a fail-closed domain validation error.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

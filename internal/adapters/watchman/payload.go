package watchman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
)

// DefaultMaxStdinBytes matches docs/examples/config.yaml limits.max_stdin_bytes.
const DefaultMaxStdinBytes int64 = 4 << 20

// Entry is one parsed payload entry. Ordinal is the position in the
// original payload and the ordering tiebreaker; digests stay empty at this
// stage because hashing belongs to E2-T3.
type Entry struct {
	Name    string
	Ordinal int
	Op      records.Operation
	Exists  bool
	Type    records.FileType
	Size    *int64 // pre-deletion size on delete entries; never a reason to open the file
}

// ChangeItem projects the entry into the canonical record (SRC-004):
// create = exists && new, modify = exists && !new, delete = !exists.
// DigestStatus is unavailable for surviving files until E2-T3 hashes them
// and not_applicable for deletes.
func (e Entry) ChangeItem() records.ChangeItem {
	status := records.DigestUnavailable
	if !e.Exists {
		status = records.DigestNotApplicable
	}
	return records.ChangeItem{
		Path:         e.Name,
		Ordinal:      e.Ordinal,
		Operation:    e.Op,
		ExistsAfter:  e.Exists,
		FileType:     e.Type,
		DigestStatus: status,
	}
}

// Input is the bounded source-input DTO produced from one trigger
// invocation: the raw payload digest over the exact stdin bytes, the
// parsed entries in payload order, and the trusted environment context.
type Input struct {
	RawDigest records.Digest
	Entries   []Entry
	Env       Env
}

// ErrStdinTooLarge reports an over-bound stdin payload so callers can
// map it to its dedicated error code (error-model source_input_too_large).
var ErrStdinTooLarge = errors.New("stdin payload exceeds the limit")

// ReadInput reads at most maxStdinBytes from stdin (SEC-009). Oversized or
// empty input fails before any payload parsing and therefore before any
// file access. The bound is checked without max+1 arithmetic so a
// MaxInt64 limit cannot overflow.
func ReadInput(stdin io.Reader, env Env, maxStdinBytes int64) (Input, error) {
	if maxStdinBytes <= 0 {
		return Input{}, fmt.Errorf("maxStdinBytes must be positive, got %d", maxStdinBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, maxStdinBytes))
	if err != nil {
		return Input{}, fmt.Errorf("reading stdin: %w", err)
	}
	var probe [1]byte
	if n, _ := io.ReadFull(stdin, probe[:]); n > 0 {
		return Input{}, fmt.Errorf("%w: exceeds %d bytes", ErrStdinTooLarge, maxStdinBytes)
	}
	if len(raw) == 0 {
		return Input{}, fmt.Errorf("stdin payload is empty")
	}
	entries, digest, err := ParsePayload(raw)
	if err != nil {
		return Input{}, err
	}
	return Input{RawDigest: digest, Entries: entries, Env: env}, nil
}

// payloadObject is the strict per-entry field set from the E0-T5 corpus:
// exactly the requestable fields, with type or mode as the mutually
// exclusive type indicator (the definition-time default set uses mode).
type payloadObject struct {
	Name   *string      `json:"name"`
	Exists *bool        `json:"exists"`
	New    *bool        `json:"new"`
	Size   *json.Number `json:"size"`
	Type   *string      `json:"type"`
	Mode   *string      `json:"mode"`
}

// ParsePayload parses a bare JSON array of Watchman file objects into
// entries and the raw payload digest. It fails closed on every shape the
// frozen corpus and its documented synthetic mutations exclude: non-array
// root, non-object elements, unknown or missing fields, invalid names,
// missing booleans, bad sizes, unknown type letters, and non-octal modes.
// Key order in elements is irrelevant (it is unstable in real responses).
// One documented exception: encoding/json collapses duplicate keys within
// an object to the last value, which then passes the same field checks, so
// duplicate keys are not themselves a rejection.
func ParsePayload(raw []byte) ([]Entry, records.Digest, error) {
	digest := records.SumDigest(raw)
	if !utf8.Valid(raw) {
		return nil, "", fmt.Errorf("payload is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var arr []payloadObject
	if err := dec.Decode(&arr); err != nil {
		return nil, "", fmt.Errorf("payload is not a JSON array of objects: %v", err)
	}
	if dec.More() {
		return nil, "", fmt.Errorf("payload has trailing content")
	}
	if len(arr) == 0 {
		// An empty match produces no invocation at all (E0-T5 §4), so an
		// empty delivered array is malformed input.
		return nil, "", fmt.Errorf("payload array is empty")
	}
	if err := checkTrailing(dec); err != nil {
		return nil, "", err
	}
	entries := make([]Entry, 0, len(arr))
	for i, obj := range arr {
		e, err := parseEntry(obj, i)
		if err != nil {
			return nil, "", fmt.Errorf("entry %d: %w", i, err)
		}
		entries = append(entries, e)
	}
	return entries, digest, nil
}

// checkTrailing rejects any second JSON document or non-whitespace tail.
func checkTrailing(dec *json.Decoder) error {
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("payload has trailing content")
	}
	return nil
}

func parseEntry(obj payloadObject, ordinal int) (Entry, error) {
	if obj.Name == nil {
		return Entry{}, fmt.Errorf("name is missing")
	}
	if obj.Exists == nil {
		return Entry{}, fmt.Errorf("exists is missing")
	}
	if obj.New == nil {
		return Entry{}, fmt.Errorf("new is missing")
	}
	name, err := records.NormalizePath(*obj.Name)
	if err != nil {
		return Entry{}, fmt.Errorf("unusable name: %v", err)
	}
	var size *int64
	if obj.Size != nil {
		// On delete entries the size is the pre-deletion size: it is kept
		// as evidence and is never a reason to open the file.
		n, err := obj.Size.Int64()
		if err != nil || n < 0 {
			return Entry{}, fmt.Errorf("size %s is not a non-negative integer", obj.Size.String())
		}
		size = &n
	}
	ft, err := fileType(obj)
	if err != nil {
		return Entry{}, err
	}
	var op records.Operation
	switch {
	case *obj.Exists && *obj.New:
		op = records.OpCreate
	case *obj.Exists && !*obj.New:
		op = records.OpModify
	default:
		op = records.OpDelete
	}
	return Entry{Name: name, Ordinal: ordinal, Op: op, Exists: *obj.Exists, Type: ft, Size: size}, nil
}

// fileType maps the type letter or octal mode to the canonical file type.
// The letters are Watchman's; unknown letters fail closed. Mode is the
// definition-time default form: a string of octal digits whose S_IFMT
// bits carry the type.
func fileType(obj payloadObject) (records.FileType, error) {
	switch {
	case obj.Type != nil && obj.Mode != nil:
		return "", fmt.Errorf("entry has both type and mode")
	case obj.Type != nil:
		switch *obj.Type {
		case "f":
			return records.FileRegular, nil
		case "d":
			return records.FileDirectory, nil
		case "l":
			return records.FileSymlink, nil
		case "b", "c":
			return records.FileOther, nil
		default:
			return "", fmt.Errorf("unknown type %q", *obj.Type)
		}
	case obj.Mode != nil:
		// The schema contract caps mode at 7 octal digits; the S_IFMT
		// type bits need no more.
		if len(*obj.Mode) > 7 {
			return "", fmt.Errorf("mode %q exceeds 7 octal digits", *obj.Mode)
		}
		mode, err := strconv.ParseUint(*obj.Mode, 8, 32)
		if err != nil {
			return "", fmt.Errorf("mode %q is not octal", *obj.Mode)
		}
		switch mode & 0o170000 {
		case 0o040000:
			return records.FileDirectory, nil
		case 0o120000:
			return records.FileSymlink, nil
		case 0o100000:
			return records.FileRegular, nil
		default:
			return records.FileOther, nil
		}
	default:
		return "", fmt.Errorf("entry has neither type nor mode")
	}
}

// Position returns the opaque position pair for the invocation.
func (in Input) Position() Position {
	return Position{Since: in.Env.Since, Clock: in.Env.Clock}
}

// SourceEventKey derives the retransmission key for a trusted source ID.
func (in Input) SourceEventKey(sourceID string) string {
	return SourceEventKey(sourceID, in.Position(), in.RawDigest)
}

// Changes projects all entries into canonical change items in payload
// order; canonical ordering is applied by the ingestion stage, not here.
func (in Input) Changes() []records.ChangeItem {
	changes := make([]records.ChangeItem, 0, len(in.Entries))
	for _, e := range in.Entries {
		changes = append(changes, e.ChangeItem())
	}
	return changes
}

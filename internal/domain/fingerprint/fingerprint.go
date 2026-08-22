// Package fingerprint derives the content fingerprint and idempotency key
// from dedicated canonical projections (domain-model §14, DAT-004,
// DAT-005, DAT-006). Both hash the UTF-8 bytes of a canonical JSON
// encoding with SHA-256; the encoder is deterministic for the projection
// field types (struct field order is fixed, arrays are pre-sorted by the
// caller, and Go string encoding is stable across supported platforms).
package fingerprint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

// Content computes the content fingerprint. Changes are sorted into
// canonical order inside this function (path, then delete/create/modify,
// then the given order as tiebreaker), so the result never depends on the
// caller's slice order; timestamps, observation IDs, and delivery attempt
// fields are excluded by the projection type itself. Every string is
// validated as strict UTF-8 before encoding so invalid bytes cannot be
// silently replaced into a collision.
func Content(in records.ContentFingerprintInput) (records.Digest, error) {
	if err := validateContentInput(in); err != nil {
		return "", err
	}
	sorted := make([]records.FingerprintChange, len(in.Changes))
	copy(sorted, in.Changes)
	// Total canonical order over every projection field, so the result is
	// independent of the caller's slice order even for duplicate
	// (path, operation) pairs.
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if ra, rb := records.OperationRank(records.Operation(a.Operation)), records.OperationRank(records.Operation(b.Operation)); ra != rb {
			return ra < rb
		}
		if a.BeforeDigest != b.BeforeDigest {
			return a.BeforeDigest < b.BeforeDigest
		}
		if a.AfterDigest != b.AfterDigest {
			return a.AfterDigest < b.AfterDigest
		}
		return !a.ExistsAfter && b.ExistsAfter
	})
	in.Changes = sorted
	enc, err := canonicalJSON(in)
	if err != nil {
		return "", fmt.Errorf("fingerprint projection: %w", err)
	}
	sum := sha256.Sum256(enc)
	return records.Digest("sha256:" + hex.EncodeToString(sum[:])), nil
}

func validateContentInput(in records.ContentFingerprintInput) error {
	if in.ResourceID == "" {
		return fmt.Errorf("resource id is required")
	}
	if !utf8.ValidString(in.ResourceID) || !utf8.ValidString(in.RelativeRoot) {
		return fmt.Errorf("projection strings must be valid UTF-8")
	}
	for i, c := range in.Changes {
		if _, err := records.NormalizePath(c.Path); err != nil {
			return fmt.Errorf("change %d: %w", i, err)
		}
		if c.Operation != "create" && c.Operation != "modify" && c.Operation != "delete" {
			return fmt.Errorf("change %d: unknown operation %q", i, c.Operation)
		}
		for _, d := range []string{c.BeforeDigest, c.AfterDigest} {
			if d == "" {
				continue
			}
			if _, err := records.ParseDigest(d); err != nil {
				return fmt.Errorf("change %d: %w", i, err)
			}
		}
	}
	return nil
}

// Idempotency computes the idempotency key in the contract form
// agent-dispatch:v1:sha256:<hex>. The projection excludes attempt number and
// submission time; callers change the key intentionally by advancing the
// generation (a rerun) while retries keep it.
func Idempotency(in records.IdempotencyKeyInput) (string, error) {
	if in.Generation < 1 {
		return "", fmt.Errorf("generation must be >= 1")
	}
	if in.Generation >= 1<<53 {
		return "", fmt.Errorf("generation exceeds the IEEE-754 safe integer range")
	}
	for _, s := range []string{in.RouteID, in.RouteRevision, in.TargetID, in.RequestVersion} {
		if s == "" {
			return "", fmt.Errorf("idempotency projection fields must be non-empty")
		}
		if !utf8.ValidString(s) {
			return "", fmt.Errorf("idempotency projection strings must be valid UTF-8")
		}
	}
	if _, err := records.ParseDigest(in.ContentFingerprint); err != nil {
		return "", fmt.Errorf("content fingerprint: %w", err)
	}
	enc, err := canonicalJSON(in)
	if err != nil {
		return "", fmt.Errorf("idempotency projection: %w", err)
	}
	sum := sha256.Sum256(enc)
	return "agent-dispatch:v1:sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonicalJSON encodes a dedicated projection struct deterministically.
// Struct fields are declared in the lexicographic order of their JSON keys
// (all ASCII), integers are limited to the IEEE-754 safe range, booleans
// are literal, and string values are validated UTF-8 beforehand.
// encoding/json escapes U+2028/U+2029 even with HTML escaping disabled;
// unescapeLineSeparators rewrites exactly those escapes back to the raw
// three-byte UTF-8 forms RFC 8785 requires, so the output is
// byte-equivalent to RFC 8785 for the projection field types. No floats
// or maps participate.
func canonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return unescapeLineSeparators(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// unescapeLineSeparators rewrites the \u2028/\u2029 escapes produced by
// encoding/json for the U+2028/U+2029 codepoints back to raw UTF-8. It
// only rewrites an escape whose introducing backslash is not itself part
// of an escaped literal backslash (which encodes as two backslash bytes),
// so a string containing the six literal characters \u2028 is untouched
// and the transform stays injective.
func unescapeLineSeparators(data []byte) []byte {
	var out []byte
	for i := 0; i < len(data); {
		if data[i] == '\\' && i+1 < len(data) && data[i+1] == '\\' {
			out = append(out, data[i], data[i+1])
			i += 2
			continue
		}
		if data[i] == '\\' && i+6 <= len(data) {
			seq := data[i : i+6]
			if bytes.Equal(seq, []byte(`\u2028`)) {
				out = append(out, 0xE2, 0x80, 0xA8)
				i += 6
				continue
			}
			if bytes.Equal(seq, []byte(`\u2029`)) {
				out = append(out, 0xE2, 0x80, 0xA9)
				i += 6
				continue
			}
		}
		out = append(out, data[i])
		i++
	}
	return out
}

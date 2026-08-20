package records

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// digestPattern is the canonical digest form (canonical-record-contracts
// §1): sha256 with exactly 64 lowercase hex characters.
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Digest is a typed content digest. Parsing fails closed on any other
// form.
type Digest string

// ParseDigest validates digest text.
func ParseDigest(s string) (Digest, error) {
	if !digestPattern.MatchString(s) {
		return "", fmt.Errorf("invalid digest %q (want sha256:<64 lowercase hex>)", s)
	}
	return Digest(s), nil
}

// String returns the canonical text form.
func (d Digest) String() string { return string(d) }

// SumDigest hashes data into a Digest.
func SumDigest(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}

// NormalizePath validates and normalizes a relative path for canonical
// ordering: forward slashes only, non-empty, no leading slash, no "." or
// ".." segments, no empty segments, no NUL, and valid UTF-8 text.
func NormalizePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	if !utf8.ValidString(p) {
		return "", fmt.Errorf("path %q is not valid UTF-8", p)
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("path %q contains NUL", p)
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("path %q must be relative", p)
	}
	if strings.ContainsRune(p, '\\') {
		return "", fmt.Errorf("path %q must use / separators", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			return "", fmt.Errorf("path %q has an empty segment", p)
		}
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("path %q has a %q segment", p, seg)
		}
	}
	return p, nil
}

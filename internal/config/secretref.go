package config

import (
	"fmt"
	"strconv"
)

// SecretRef is a parsed, unresolved secret reference (configuration-spec
// §11, SEC-006). The loader stores the reference only; resolution happens
// immediately before use by the future secretresolver adapter, and the
// resolved value never enters logs, output, or SQLite.
type SecretRef struct {
	Kind SecretRefKind
	Name string // env variable name, keychain reference, or fd number text
	Path string // file reference absolute path
	Text string // original reference text
}

// SecretRefKind enumerates the reference forms.
type SecretRefKind string

const (
	RefEnv      SecretRefKind = "env"
	RefFile     SecretRefKind = "file"
	RefKeychain SecretRefKind = "keychain"
	RefFD       SecretRefKind = "fd"
)

// ParseSecretRef parses a secret reference without resolving it. All four
// forms are accepted; anything else fails closed.
func ParseSecretRef(text string) (*SecretRef, error) {
	switch {
	case hasPrefix(text, "env:"):
		name := text[len("env:"):]
		if !validEnvName(name) {
			return nil, fmt.Errorf("invalid env reference %q", text)
		}
		return &SecretRef{Kind: RefEnv, Name: name, Text: text}, nil
	case hasPrefix(text, "file:"):
		path := text[len("file:"):]
		if len(path) == 0 || path[0] != '/' || !validNoSpace(path) {
			return nil, fmt.Errorf("invalid file reference %q (want file:/absolute/path)", text)
		}
		return &SecretRef{Kind: RefFile, Path: path, Text: text}, nil
	case hasPrefix(text, "keychain:"):
		ref := text[len("keychain:"):]
		if ref == "" || !validNoSpace(ref) {
			return nil, fmt.Errorf("invalid keychain reference %q", text)
		}
		return &SecretRef{Kind: RefKeychain, Name: ref, Text: text}, nil
	case hasPrefix(text, "fd:"):
		num := text[len("fd:"):]
		if !allDigits(num) || num[0] == '0' {
			return nil, fmt.Errorf("invalid fd reference %q (want fd:<positive-integer> without leading zeros)", text)
		}
		if n, err := strconv.Atoi(num); err != nil || n < 1 {
			return nil, fmt.Errorf("invalid fd reference %q (want fd:<positive-integer>)", text)
		}
		return &SecretRef{Kind: RefFD, Name: num, Text: text}, nil
	}
	return nil, fmt.Errorf("unsupported secret reference %q (want env:NAME, file:/path, keychain:ref, or fd:N)", text)
}

// Redacted returns the reference identifier safe for display; the value is
// never present because it is never resolved here.
func (s *SecretRef) Redacted() string { return s.Text }

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validNoSpace(s string) bool {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return false
		}
	}
	return true
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

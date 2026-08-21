// Package ids provides the injectable unique-ID abstraction for domain
// records (DAT-002, implementation-guide §7): production uses the UUIDv7
// generator, tests inject deterministic generators so behavior never
// depends on wall-clock randomness.
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ID is a time-ordered unique identifier (UUIDv7 text form). Storage must
// not depend on any prefix (canonical-record-contracts §1).
type ID string

// String returns the canonical lowercase hyphenated form.
func (id ID) String() string { return string(id) }

// Generator creates unique time-ordered IDs.
type Generator interface {
	NewID() (ID, error)
}

// UUIDv7 is the production generator.
type UUIDv7 struct {
	now func() time.Time
}

// NewUUIDv7 returns a generator using the given clock (nil means
// time.Now).
func NewUUIDv7(now func() time.Time) *UUIDv7 {
	if now == nil {
		now = time.Now
	}
	return &UUIDv7{now: now}
}

// NewID produces a UUIDv7 identifier: 48-bit millisecond timestamp, version
// 7, variant 10x, 74 random bits.
func (g *UUIDv7) NewID() (ID, error) {
	var b [16]byte
	ms := uint64(g.now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		return "", fmt.Errorf("uuidv7 entropy: %w", err)
	}
	b[6] = 0x70 | (b[6] & 0x0f) // version 7
	b[8] = 0x80 | (b[8] & 0x3f) // RFC 4122 variant
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return ID(strings.ToLower(string(out[:]))), nil
}

// ParseID validates an external identifier: the canonical observation IDs
// are UUIDv7 with at least 16 characters (source-observation schema), but
// generic record IDs accept any non-empty trimmed string because storage
// must not depend on prefixes.
func ParseID(text string) (ID, error) {
	if strings.TrimSpace(text) != text || len(text) < 1 {
		return "", errors.New("id must be non-empty and trimmed")
	}
	return ID(text), nil
}

// ParseUUIDv7 parses and structurally validates a UUIDv7 text form.
func ParseUUIDv7(text string) (ID, error) {
	t := strings.ToLower(strings.TrimSpace(text))
	if len(t) != 36 || t[8] != '-' || t[13] != '-' || t[18] != '-' || t[23] != '-' {
		return "", fmt.Errorf("invalid uuid text form %q", text)
	}
	var sb strings.Builder
	for i, r := range t {
		if (i == 8 || i == 13 || i == 18 || i == 23) != (r == '-') {
			return "", fmt.Errorf("invalid uuid hyphen placement %q", text)
		}
		if r != '-' {
			sb.WriteRune(r)
		}
	}
	var raw [16]byte
	if _, err := hex.Decode(raw[:], []byte(sb.String())); err != nil {
		return "", fmt.Errorf("invalid uuid hex %q: %v", text, err)
	}
	if raw[6]>>4 != 7 {
		return "", fmt.Errorf("not a uuid version 7: %q", text)
	}
	if raw[8]>>6 != 2 {
		return "", fmt.Errorf("invalid uuid variant: %q", text)
	}
	return ID(t), nil
}

// Sequential is a deterministic generator for tests.
type Sequential struct {
	next uint64
}

// NewID yields id-000001 style identifiers in order.
func (s *Sequential) NewID() (ID, error) {
	s.next++
	return ID(fmt.Sprintf("id-%08d", s.next)), nil
}

// RandomSuffix renders four random bytes as eight hex characters,
// making same-second identifiers distinct by construction. A
// randomness failure degrades to a fixed suffix rather than an error:
// callers treat it as uniqueness best-effort, never as proof.
func RandomSuffix() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

// CompactTimestamp strips the separators from a canonical RFC 3339
// timestamp for use inside identifiers.
func CompactTimestamp(now string) string {
	out := make([]byte, 0, len(now))
	for i := 0; i < len(now); i++ {
		switch c := now[i]; c {
		case ':', '-', 'T', 'Z', '.':
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

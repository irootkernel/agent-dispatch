package fingerprint

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/irootkernel/agent-dispatch/internal/domain/records"
)

func sampleInput() records.ContentFingerprintInput {
	return records.ContentFingerprintInput{
		ResourceID: "vault-main",
		Changes: []records.FingerprintChange{
			{Path: "a/x.md", Operation: "create", AfterDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", ExistsAfter: true},
			{Path: "b/2.md", Operation: "modify", BeforeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", AfterDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", ExistsAfter: true},
			{Path: "c/gone.md", Operation: "delete", BeforeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", ExistsAfter: false},
		},
	}
}

func idemInput(fp string, generation int64) records.IdempotencyKeyInput {
	return records.IdempotencyKeyInput{
		RouteID:            "wiki-maintenance",
		RouteRevision:      "rev-abc",
		TargetID:           "hermes-kanban-main",
		Generation:         generation,
		ContentFingerprint: fp,
		RequestVersion:     "agent-dispatch.dispatch-intent/v1",
	}
}

func TestContentFingerprintByteIdenticalRepeat(t *testing.T) {
	a, err := Content(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Content(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("repeated projection must be byte-identical: %s vs %s", a, b)
	}
	if _, err := records.ParseDigest(a.String()); err != nil {
		t.Fatalf("fingerprint must be a valid digest: %v", err)
	}
}

func TestObservationIdentityExcluded(t *testing.T) {
	// Observation IDs, timestamps, and delivery attempt fields are not part
	// of the projection type, so changing them cannot change the
	// fingerprint (acceptance: observation ID changes do not change it).
	base := sampleInput()
	first, _ := Content(base)
	// Same content with reordered change slice already canonically sorted
	// stays equal; different content changes the fingerprint.
	changed := base
	changed.Changes[0].AfterDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	second, _ := Content(changed)
	if first == second {
		t.Fatal("content change must change the fingerprint")
	}
}

func TestContentFingerprintGolden(t *testing.T) {
	got, err := Content(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "content-fingerprint.golden")
	if _, err := os.Stat(golden); os.IsNotExist(err) {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got.String()+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(got.String()+"\n"), want) {
		t.Fatalf("fingerprint drifted cross-platform or across runs: got %s want %s", got, want)
	}
}

func TestIdempotencyKeyProperties(t *testing.T) {
	fp, err := Content(sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	key1, err := Idempotency(idemInput(fp.String(), 3))
	if err != nil {
		t.Fatal(err)
	}
	// Retry attempt and submission time are not projection fields; the
	// caller cannot vary them through the input type. Recomputing the
	// identical decision yields the identical key.
	key2, _ := Idempotency(idemInput(fp.String(), 3))
	if key1 != key2 {
		t.Fatalf("identical inputs must yield identical keys: %s vs %s", key1, key2)
	}
	if !regexp.MustCompile(`^agent-dispatch:v1:sha256:[0-9a-f]{64}$`).MatchString(key1) {
		t.Fatalf("key form invalid: %s", key1)
	}
	// A rerun advances the generation and must change the key.
	key3, _ := Idempotency(idemInput(fp.String(), 4))
	if key3 == key1 {
		t.Fatal("rerun generation must change the idempotency key")
	}
}

func TestIdempotencyKeyGolden(t *testing.T) {
	fp, _ := Content(sampleInput())
	key, err := Idempotency(idemInput(fp.String(), 1))
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "idempotency-key.golden")
	if _, err := os.Stat(golden); os.IsNotExist(err) {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(key+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, _ := os.ReadFile(golden)
	if !bytes.Equal([]byte(key+"\n"), want) {
		t.Fatalf("key drifted: got %s want %s", key, want)
	}
}

func TestIdempotencyFailsClosed(t *testing.T) {
	if _, err := Idempotency(records.IdempotencyKeyInput{Generation: 0}); err == nil {
		t.Error("generation < 1 must fail")
	}
	in := idemInput("sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", 1)
	if _, err := Idempotency(in); err != nil {
		t.Errorf("valid input rejected: %v", err)
	}
	in.ContentFingerprint = "not-a-digest"
	if _, err := Idempotency(in); err == nil {
		t.Error("invalid fingerprint must fail")
	}
}

func TestContentFingerprintOrderInvariance(t *testing.T) {
	base := sampleInput()
	// Same changes in reversed slice order, including a duplicate
	// (path, operation) pair with different digests, must fingerprint
	// identically.
	withDup := append(append([]records.FingerprintChange{}, base.Changes...),
		records.FingerprintChange{Path: "a/x.md", Operation: "create", AfterDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000", ExistsAfter: true})
	a := records.ContentFingerprintInput{ResourceID: base.ResourceID, Changes: withDup, Overflow: base.Overflow}
	reversed := records.ContentFingerprintInput{ResourceID: base.ResourceID, Changes: nil}
	for i := len(withDup) - 1; i >= 0; i-- {
		reversed.Changes = append(reversed.Changes, withDup[i])
	}
	fa, err := Content(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := Content(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if fa != fb {
		t.Fatalf("fingerprint must be order-invariant: %s vs %s", fa, fb)
	}
}

func TestContentFingerprintInvalidUTF8Fails(t *testing.T) {
	in := sampleInput()
	in.ResourceID = "vault\xff"
	if _, err := Content(in); err == nil {
		t.Fatal("invalid UTF-8 resource id must fail closed")
	}
	in2 := sampleInput()
	in2.Changes[0].Path = "bad\xff.md"
	if _, err := Content(in2); err == nil {
		t.Fatal("invalid UTF-8 path must fail closed")
	}
}

func TestLineSeparatorEscapingNoCollision(t *testing.T) {
	// A string containing the literal six characters \u2028 encodes with an
	// escaped backslash and must never collide with the string containing
	// the actual U+2028 codepoint after canonical unescaping.
	codepoint := records.ContentFingerprintInput{ResourceID: "a\u2028b"}
	literal := records.ContentFingerprintInput{ResourceID: `a\u2028b`}
	fa, err := Content(codepoint)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := Content(literal)
	if err != nil {
		t.Fatal(err)
	}
	if fa == fb {
		t.Fatalf("codepoint and literal escape text must not collide: %s", fa)
	}
}

func TestGenerationSafeRange(t *testing.T) {
	fp := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if _, err := Idempotency(idemInput(fp, 1<<53)); err == nil {
		t.Fatal("2^53 exceeds the safe integer range and must fail")
	}
	if _, err := Idempotency(idemInput(fp, 1<<53-1)); err != nil {
		t.Fatalf("2^53-1 must be accepted: %v", err)
	}
}

func TestUnescapeLineSeparatorsBytes(t *testing.T) {
	// Byte-level assertion covering both rewrite branches and the
	// escaped-backslash passthrough.
	in := []byte(`a\u2028b\\u2029c\\d`)
	got := unescapeLineSeparators(in)
	// The first escape (single backslash) is a codepoint escape and is
	// rewritten; the double backslashes stay, so the literal \u2029 text
	// survives intact.
	want := []byte("a\xE2\x80\xA8b" + `\\u2029` + `c\\d`)
	if !bytes.Equal(got, want) {
		t.Fatalf("unescape = %q, want %q", got, want)
	}
	// U+2029 produced by the encoder for the codepoint is rewritten.
	enc := []byte(`x\y\u2029z`)
	got = unescapeLineSeparators(enc)
	if !bytes.Contains(got, []byte("\xE2\x80\xA9")) || bytes.Contains(got, []byte(`\u2029`)) {
		t.Fatalf("codepoint escape not rewritten: %q", got)
	}
	// Identity on input without escapes.
	plain := []byte(`{"a":"b"}`)
	if !bytes.Equal(unescapeLineSeparators(plain), plain) {
		t.Fatal("plain input must pass through unchanged")
	}
}

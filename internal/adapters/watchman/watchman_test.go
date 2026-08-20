package watchman

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootkernel/jjukkumi/internal/domain/records"
)

// fixtureDir points at the frozen E0-T5 corpus so parser assumptions stay
// traceable to real payloads (TST-003).
const fixtureDir = "../../../docs/integrations/fixtures/watchman"

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// TestFrozenTriggerPayloadsParse proves every frozen real payload parses
// and that the canonical operation mapping holds per the E0-T5 report §4.
func TestFrozenTriggerPayloadsParse(t *testing.T) {
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("read fixture dir: %v", err)
	}
	parsed := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "trigger-payload-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw := mustRead(t, filepath.Join(fixtureDir, name))
		got, digest, err := ParsePayload(raw)
		if err != nil {
			t.Fatalf("%s: unexpected parse failure: %v", name, err)
		}
		if len(got) == 0 {
			t.Fatalf("%s: no entries", name)
		}
		if _, err := records.ParseDigest(string(digest)); err != nil {
			t.Fatalf("%s: digest %q invalid: %v", name, digest, err)
		}
		parsed++
	}
	if parsed < 18 {
		t.Fatalf("expected the full frozen trigger corpus (>=18 payloads), got %d", parsed)
	}
}

func parseFixture(t *testing.T, name string) []Entry {
	t.Helper()
	entries, _, err := ParsePayload(mustRead(t, filepath.Join(fixtureDir, name)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return entries
}

func TestOperationMappingPerFixtures(t *testing.T) {
	if e := parseFixture(t, "trigger-payload-create.json"); e[0].Op != records.OpCreate || !e[0].Exists {
		t.Fatalf("create fixture mapped wrong: %+v", e[0])
	}
	if e := parseFixture(t, "trigger-payload-modify.json"); e[0].Op != records.OpModify || !e[0].Exists {
		t.Fatalf("modify fixture mapped wrong: %+v", e[0])
	}
	del := parseFixture(t, "trigger-payload-delete.json")[0]
	if del.Op != records.OpDelete || del.Exists || del.Size == nil {
		t.Fatalf("delete fixture mapped wrong: %+v", del)
	}
	item := del.ChangeItem()
	if item.ExistsAfter || item.DigestStatus != records.DigestNotApplicable {
		t.Fatalf("delete change item wrong: %+v", item)
	}
	rep := parseFixture(t, "trigger-payload-replacement.json")
	ops := map[string]records.Operation{}
	for _, e := range rep {
		ops[e.Name] = e.Op
	}
	if ops["repl-src.md"] != records.OpDelete || ops["trigger-repeated.md"] != records.OpModify {
		t.Fatalf("replacement mapping wrong: %v", ops)
	}
}

func TestBulkFixtureEntryCountAndUnsortedOrder(t *testing.T) {
	entries := parseFixture(t, "trigger-payload-bulk-60.json")
	if len(entries) != 60 {
		t.Fatalf("bulk fixture should carry 60 entries, got %d", len(entries))
	}
	sorted := true
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Name > entries[i].Name {
			sorted = false
			break
		}
	}
	if sorted {
		t.Fatal("bulk fixture arrived sorted; the corpus records arbitrary order, re-freeze evidence")
	}
	for i, e := range entries {
		if e.Ordinal != i {
			t.Fatalf("ordinal must be payload position: entry %d has %d", i, e.Ordinal)
		}
	}
}

func TestAtomicSaveOmitsTempName(t *testing.T) {
	entries := parseFixture(t, "trigger-payload-atomic-save.json")
	if len(entries) != 1 {
		t.Fatalf("atomic save should deliver one final-path entry, got %d", len(entries))
	}
	if entries[0].Name != "trigger-create.md" || entries[0].Op != records.OpModify || !entries[0].Exists {
		t.Fatalf("atomic save must deliver exactly the final path as an existing modify: %+v", entries[0])
	}
}

// TestRepeatedSaveFixturesAreDeliveredEveryTime proves same-content
// changes are re-delivered on every save (E0-T5 §4): the first save of a
// new path is a create, later saves are modifies, and none are suppressed.
func TestRepeatedSaveFixturesAreDeliveredEveryTime(t *testing.T) {
	wantOps := []records.Operation{records.OpCreate, records.OpModify, records.OpModify}
	for i, want := range wantOps {
		entries := parseFixture(t, fmt.Sprintf("trigger-payload-repeated-save-%d.json", i+1))
		if len(entries) != 1 || entries[0].Op != want {
			t.Fatalf("repeated save %d: want %s, got %+v", i+1, want, entries)
		}
	}
}

func TestTypeLetterEnumMapped(t *testing.T) {
	want := map[string]records.FileType{
		"f": records.FileRegular,
		"d": records.FileDirectory,
		"l": records.FileSymlink,
		"b": records.FileOther,
		"c": records.FileOther,
	}
	for letter, wantType := range want {
		entries, _, err := ParsePayload([]byte(`[{"name":"a","exists":true,"new":true,"size":1,"type":"` + letter + `"}]`))
		if err != nil {
			t.Fatalf("type %q: %v", letter, err)
		}
		if entries[0].Type != wantType {
			t.Fatalf("type %q mapped to %s, want %s", letter, entries[0].Type, wantType)
		}
	}
	if _, _, err := ParsePayload([]byte(`[{"name":"a","exists":true,"new":true,"size":1,"type":"x"}]`)); err == nil {
		t.Fatal("unknown type letter must fail closed")
	}
}

// TestContradictoryFlagsMapToDelete documents the chosen semantics for the
// contradictory !exists && new combination: existence wins and the entry is
// a delete. A future tightening to fail-closed must be a deliberate,
// tested change.
func TestContradictoryFlagsMapToDelete(t *testing.T) {
	entries, _, err := ParsePayload([]byte(`[{"name":"gone.md","exists":false,"new":true,"size":1,"type":"f"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Op != records.OpDelete {
		t.Fatalf("contradictory flags must map to delete, got %s", entries[0].Op)
	}
}

func TestModeLengthCappedAtSevenOctalDigits(t *testing.T) {
	if _, _, err := ParsePayload([]byte(`[{"name":"a","exists":true,"new":true,"size":1,"mode":"100064444"}]`)); err == nil {
		t.Fatal("mode longer than 7 octal digits must fail (schema contract)")
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadInputFailingReaderSurfacesError(t *testing.T) {
	sentinel := errors.New("broken pipe")
	if _, err := ReadInput(errReader{sentinel}, Env{}, DefaultMaxStdinBytes); err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("reader failure must surface, got %v", err)
	}
}

func TestReadInputRejectsNonPositiveLimit(t *testing.T) {
	if _, err := ReadInput(bytes.NewReader([]byte(`[]`)), Env{}, 0); err == nil {
		t.Fatal("maxStdinBytes <= 0 must fail closed, not fall back")
	}
}

func TestModeFormTypeMapping(t *testing.T) {
	cases := map[string]records.FileType{
		"100644": records.FileRegular,
		"040755": records.FileDirectory,
		"120777": records.FileSymlink,
		"060000": records.FileOther,
	}
	for mode, want := range cases {
		entries, _, err := ParsePayload([]byte(`[{"name":"a","exists":true,"new":true,"size":1,"mode":"` + mode + `"}]`))
		if err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
		if entries[0].Type != want {
			t.Fatalf("mode %s mapped to %s, want %s", mode, entries[0].Type, want)
		}
	}
}

// TestMalformedFixturesFailClosed covers the synthetic malformed classes
// derived from the frozen shapes (E0-T5 report §9). Every case must fail
// before any file access, which the parser guarantees by never touching
// the filesystem.
func TestMalformedFixturesFailClosed(t *testing.T) {
	files, err := os.ReadDir("testdata/malformed")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 15 {
		t.Fatalf("expected the malformed corpus, got %d files", len(files))
	}
	for _, f := range files {
		raw := mustRead(t, filepath.Join("testdata/malformed", f.Name()))
		if _, _, err := ParsePayload(raw); err == nil {
			t.Fatalf("%s: malformed payload must fail closed", f.Name())
		}
	}
}

func TestParsePayloadRejectsInvalidUTF8(t *testing.T) {
	if _, _, err := ParsePayload([]byte{'[', 0xff, ']'}); err == nil {
		t.Fatal("invalid UTF-8 payload must fail")
	}
}

func TestReadInputOversizedFailsBeforeParse(t *testing.T) {
	entry := `{"name":"a.md","exists":true,"new":true,"size":1,"type":"f"},`
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 100; i++ {
		sb.WriteString(entry)
	}
	sb.WriteString(`{"name":"z.md","exists":true,"new":true,"size":1,"type":"f"}]`)
	payload := []byte(sb.String())
	if int64(len(payload)) < 2000 {
		t.Fatalf("test payload unexpectedly small: %d", len(payload))
	}
	if _, err := ReadInput(bytes.NewReader(payload), Env{}, 2048); err == nil {
		t.Fatal("oversized stdin must fail")
	}
	if _, err := ReadInput(bytes.NewReader(payload), Env{}, int64(len(payload)-1)); err == nil {
		t.Fatal("stdin one byte over the bound must fail")
	}
	if _, err := ReadInput(bytes.NewReader(payload), Env{}, int64(len(payload))); err != nil {
		t.Fatalf("payload exactly at the bound must parse: %v", err)
	}
}

func TestReadInputEmptyStdinFails(t *testing.T) {
	if _, err := ReadInput(strings.NewReader(""), Env{}, DefaultMaxStdinBytes); err == nil {
		t.Fatal("empty stdin must fail")
	}
}

func TestParseEnvRequiredAndOptional(t *testing.T) {
	base := map[string]string{
		"WATCHMAN_TRIGGER": "jjukkumi-e0t5",
		"WATCHMAN_ROOT":    "/vault",
		"WATCHMAN_CLOCK":   "c:1:2:3:4",
	}
	get := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	env, err := ParseEnv(get(base))
	if err != nil {
		t.Fatal(err)
	}
	if env.Since != "" || env.HasRelative {
		t.Fatalf("minimal env wrong: %+v", env)
	}
	flags := env.Flags()
	if !flags.FreshInstance || flags.Overflow {
		t.Fatalf("missing position must be fresh-instance, never overflow: %+v", flags)
	}
	full := map[string]string{}
	for k, v := range base {
		full[k] = v
	}
	full["WATCHMAN_SINCE"] = "c:0:9:9:9"
	full["WATCHMAN_RELATIVE_ROOT"] = "/vault/Notes"
	full["WATCHMAN_SOCK"] = "/sock"
	full["WATCHMAN_FILES_OVERFLOW"] = "1"
	env, err = ParseEnv(get(full))
	if err != nil {
		t.Fatal(err)
	}
	if env.Since == "" || !env.HasRelative || env.RelativeRoot != "/vault/Notes" {
		t.Fatalf("full env wrong: %+v", env)
	}
	flags = env.Flags()
	if flags.FreshInstance || flags.Overflow || !flags.HasRelative {
		t.Fatalf("incremental env flags wrong: %+v", flags)
	}
	for _, missing := range []string{"WATCHMAN_TRIGGER", "WATCHMAN_ROOT", "WATCHMAN_CLOCK"} {
		broken := map[string]string{}
		for k, v := range base {
			broken[k] = v
		}
		delete(broken, missing)
		if _, err := ParseEnv(get(broken)); err == nil {
			t.Fatalf("%s missing must fail", missing)
		}
	}
	// Every required and optional variable fails on an empty value and on
	// an over-limit value; the allowlist rule is uniform.
	for _, name := range []string{"WATCHMAN_TRIGGER", "WATCHMAN_ROOT", "WATCHMAN_CLOCK", "WATCHMAN_SINCE", "WATCHMAN_RELATIVE_ROOT", "WATCHMAN_SOCK"} {
		empty := map[string]string{}
		for k, v := range full {
			empty[k] = v
		}
		empty[name] = ""
		if _, err := ParseEnv(get(empty)); err == nil {
			t.Fatalf("%s set empty must fail", name)
		}
		atBound := map[string]string{}
		for k, v := range full {
			atBound[k] = v
		}
		atBound[name] = strings.Repeat("x", MaxEnvValueBytes)
		if _, err := ParseEnv(get(atBound)); err != nil {
			t.Fatalf("%s exactly at the bound must be accepted: %v", name, err)
		}
		oversize := map[string]string{}
		for k, v := range full {
			oversize[k] = v
		}
		oversize[name] = strings.Repeat("x", MaxEnvValueBytes+1)
		if _, err := ParseEnv(get(oversize)); err == nil {
			t.Fatalf("%s over the bound must fail", name)
		}
	}
}

func TestSourceEventKeyPositionRule(t *testing.T) {
	digest := records.SumDigest([]byte("[]"))
	pos := Position{Since: "c:0:1:2:3", Clock: "c:0:1:2:4"}
	key := SourceEventKey("src-1", pos, digest)
	want := "watchman:src-1:c:0:1:2:3:c:0:1:2:4:" + digest.String()
	if key != want {
		t.Fatalf("key %q want %q", key, want)
	}
	if SourceEventKey("src-1", Position{Clock: "c"}, digest) != "" {
		t.Fatal("missing position must yield an empty (null) key")
	}
}

func TestValidateBindingTrustedConfigOnly(t *testing.T) {
	env := Env{Trigger: "trig", Root: "/vault/a"}
	if err := ValidateBinding(env, "trig", "/vault/a"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinding(env, "trig", "/vault/a/"); err != nil {
		t.Fatalf("trailing separator should be cleaned: %v", err)
	}
	if err := ValidateBinding(env, "other", "/vault/a"); err == nil {
		t.Fatal("trigger mismatch must fail")
	}
	if err := ValidateBinding(env, "trig", "/vault/b"); err == nil {
		t.Fatal("root mismatch must fail")
	}
}

func TestDigestIsOverExactRawBytes(t *testing.T) {
	a := []byte(`[{"name":"a.md","exists":true,"new":true,"size":1,"type":"f"}]`)
	b := []byte(` [ { "name" : "a.md" , "exists" : true , "new" : true , "size" : 1 , "type" : "f" } ] `)
	_, da, err := ParsePayload(a)
	if err != nil {
		t.Fatal(err)
	}
	_, db, err := ParsePayload(b)
	if err != nil {
		t.Fatal(err)
	}
	if da == "" || da == db {
		t.Fatal("whitespace-different payloads must hash differently; digest covers exact raw bytes")
	}
}

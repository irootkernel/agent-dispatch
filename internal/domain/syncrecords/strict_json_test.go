package syncrecords

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func strictRecordTypes() map[string]reflect.Type {
	return map[string]reflect.Type{
		"sync-membership":      reflect.TypeFor[Membership](),
		"sync-membership-plan": reflect.TypeFor[MembershipPlan](),
		"sync-publication":     reflect.TypeFor[Publication](),
		"sync-checkpoint":      reflect.TypeFor[Checkpoint](),
		"sync-checkpoint-plan": reflect.TypeFor[CheckpointPlan](),
		"sync-nudge":           reflect.TypeFor[Nudge](),
		"sync-import":          reflect.TypeFor[Import](),
		"sync-status-request":  reflect.TypeFor[StatusRequest](),
		"sync-status-response": reflect.TypeFor[StatusResponse](),
		"sync-verification":    reflect.TypeFor[Verification](),
	}
}

func strictRecordFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "examples", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func replaceStrictJSON(t *testing.T, raw []byte, old, replacement string) []byte {
	t.Helper()
	if !bytes.Contains(raw, []byte(old)) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return bytes.Replace(raw, []byte(old), []byte(replacement), 1)
}

func requireStrictRejection(t *testing.T, raw []byte, typ reflect.Type, message string) {
	t.Helper()
	out := reflect.New(typ)
	err := decodeStrict(raw, out.Interface())
	if !errors.Is(err, ErrInvalidRecord) || !strings.Contains(err.Error(), message) {
		t.Fatalf("expected invalid record containing %q, got %v", message, err)
	}
	if !out.Elem().IsZero() {
		t.Fatalf("rejected input reached typed decoding: %+v", out.Elem().Interface())
	}
}

func TestDecodeStrictExactKeysPreserveAllRecords(t *testing.T) {
	for name, typ := range strictRecordTypes() {
		t.Run(name, func(t *testing.T) {
			raw := strictRecordFixture(t, name)
			want := reflect.New(typ)
			if err := json.Unmarshal(raw, want.Interface()); err != nil {
				t.Fatal(err)
			}
			for label, input := range map[string][]byte{
				"exact tags":        raw,
				"escaped exact tag": replaceStrictJSON(t, raw, `"group_id"`, `"group\u005fid"`),
			} {
				t.Run(label, func(t *testing.T) {
					got := reflect.New(typ)
					if err := decodeStrict(input, got.Interface()); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got.Interface(), want.Interface()) {
						t.Fatalf("record values changed: got %+v, want %+v", got.Elem().Interface(), want.Elem().Interface())
					}
				})
			}
		})
	}
}

func TestDecodeStrictRejectsCaseAliasesAndDuplicates(t *testing.T) {
	for name, typ := range strictRecordTypes() {
		t.Run(name, func(t *testing.T) {
			raw := strictRecordFixture(t, name)
			for _, tc := range []struct {
				name, replacement, message string
			}{
				{"uppercase alias alone", `"GROUP_ID"`, "unknown JSON field"},
				{"mixed case alias alone", `"Group_Id"`, "unknown JSON field"},
				{"escaped alias alone", `"GROUP\u005fID"`, "unknown JSON field"},
				{"contradictory alias first", `"GROUP_ID":"other-pair","group_id"`, "unknown JSON field"},
				{"contradictory alias last", `"group_id":"other-pair","GROUP_ID"`, "unknown JSON field"},
				{"exact duplicate", `"group_id":"other-pair","group_id"`, "duplicate"},
				{"escaped exact duplicate", `"group\u005fid":"other-pair","group_id"`, "duplicate"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					input := replaceStrictJSON(t, raw, `"group_id"`, tc.replacement)
					requireStrictRejection(t, input, typ, tc.message)
				})
			}
		})
	}
}

func TestDecodeStrictRejectsNestedAliasesAndDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name, record, field string
	}{
		{"active member", "sync-membership", "instance_id"},
		{"historical member", "sync-membership", "reason"},
		{"proposed membership", "sync-membership-plan", "mode"},
		{"proposed active member", "sync-membership-plan", "instance_id"},
		{"proposed checkpoint", "sync-checkpoint-plan", "target_commit"},
		{"import path", "sync-import", "path"},
		{"expected node", "sync-verification", "instance_id"},
		{"verification node", "sync-verification", "evidence_fresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := strictRecordFixture(t, tc.record)
			typ := strictRecordTypes()[tc.record]
			key := `"` + tc.field + `"`
			t.Run("alias", func(t *testing.T) {
				input := replaceStrictJSON(t, raw, key, `"`+strings.ToUpper(tc.field)+`"`)
				requireStrictRejection(t, input, typ, "unknown JSON field")
			})
			t.Run("exact duplicate", func(t *testing.T) {
				input := replaceStrictJSON(t, raw, key, key+`:null,`+key)
				requireStrictRejection(t, input, typ, "duplicate")
			})
		})
	}
}

func TestDecodeStrictRetainsInputSafety(t *testing.T) {
	raw := strictRecordFixture(t, "sync-membership")
	typ := reflect.TypeFor[Membership]()
	for _, tc := range []struct {
		name, message string
		input         []byte
	}{
		{"empty", "empty, oversized, or invalid UTF-8", nil},
		{"oversized", "empty, oversized, or invalid UTF-8", bytes.Repeat([]byte(" "), MaxRecordBytes+1)},
		{"invalid UTF-8", "empty, oversized, or invalid UTF-8", replaceStrictJSON(t, raw, "wiki-pair", "wiki-\xffpair")},
		{"unknown field", "unknown JSON field", replaceStrictJSON(t, raw, `"group_id"`, `"unexpected":true,"group_id"`)},
		{"nested unknown field", "unknown JSON field", replaceStrictJSON(t, raw, `"instance_id"`, `"unexpected":true,"instance_id"`)},
		{"trailing object", "one value", append(bytes.Clone(raw), []byte(` {}`)...)},
		{"trailing scalar", "one value", append(bytes.Clone(raw), []byte(` true`)...)},
		{"trailing garbage", "one value", append(bytes.Clone(raw), []byte(` invalid`)...)},
		{"malformed JSON", "JSON", bytes.TrimSpace(raw)[:len(bytes.TrimSpace(raw))-1]},
		{"object in scalar field", "unexpected JSON object", replaceStrictJSON(t, raw, `"wiki-pair"`, `{"GROUP_ID":"wiki-pair"}`)},
		{"array in scalar field", "unexpected JSON array", replaceStrictJSON(t, raw, `"wiki-pair"`, `[]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireStrictRejection(t, tc.input, typ, tc.message)
		})
	}
	bounded := append(bytes.Clone(raw), bytes.Repeat([]byte(" "), MaxRecordBytes-len(raw))...)
	var out Membership
	if err := decodeStrict(bounded, &out); err != nil {
		t.Fatalf("record at size limit rejected: %v", err)
	}
	if err := decodeStrict(replaceStrictJSON(t, raw, `"wiki-pair"`, `42`), &out); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("typed scalar validation lost: %v", err)
	}
}

func TestDecodeMembershipRejectsAliasOverride(t *testing.T) {
	plan, err := NewPlan("bootstrap", "", nil, nil, testMembers(), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := CanonicalMembership(plan.ProposedMembership)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeMembership(raw); err != nil || !reflect.DeepEqual(got, plan.ProposedMembership) {
		t.Fatalf("normal membership failed round trip: %+v %v", got, err)
	}
	input := replaceStrictJSON(t, raw, `"group_id":"wiki-pair"`, `"group_id":"other-pair","GROUP_ID":"wiki-pair"`)
	if _, err := DecodeMembership(input); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("contradictory alias silently overrode membership binding: %v", err)
	}
}

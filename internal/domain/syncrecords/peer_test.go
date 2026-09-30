package syncrecords

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStatusResponseRequiresSafetyFields(t *testing.T) {
	response := StatusResponse{
		SchemaVersion: StatusResponseSchema, GroupID: "wiki-pair", Responder: "node-b",
		StateIncarnationID: "node-b-0001", MembershipRevision: strings.Repeat("1", 40),
		ContentRef: "refs/heads/wiki-sync", TargetCommit: strings.Repeat("2", 40),
		ScopeDigest: "sha256:" + strings.Repeat("a", 64), ContractDigest: "sha256:" + strings.Repeat("b", 64),
		Nonce: "0123456789abcdef", EvidenceGeneration: time.Now().UnixNano(), State: "applied",
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := DecodeStatusResponse(raw); err != nil || decoded != response {
		t.Fatalf("explicit false and zero values must survive: %+v %v", decoded, err)
	}
	for _, field := range []string{"evidence_generation", "evidence_age_seconds", "governed_dirty", "pending_work", "membership_current", "uncertain"} {
		for _, value := range []string{"missing", "null", "\"wrong-type\""} {
			t.Run(field+"/"+value, func(t *testing.T) {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				if value == "missing" {
					delete(fields, field)
				} else {
					fields[field] = json.RawMessage(value)
				}
				malformed, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := DecodeStatusResponse(malformed); !errors.Is(err, ErrInvalidRecord) {
					t.Fatalf("malformed safety field accepted: %v", err)
				}
			})
		}
	}
}

func TestStatusResponseDecodeRejectsOversizeAndUnknownFields(t *testing.T) {
	response := StatusResponse{
		SchemaVersion: StatusResponseSchema, GroupID: "wiki-pair", Responder: "node-b",
		StateIncarnationID: "node-b-0001", MembershipRevision: strings.Repeat("1", 40),
		ContentRef: "refs/heads/wiki-sync", TargetCommit: strings.Repeat("2", 40),
		ScopeDigest: "sha256:" + strings.Repeat("a", 64), ContractDigest: "sha256:" + strings.Repeat("b", 64),
		Nonce: "0123456789abcdef", EvidenceGeneration: time.Now().UnixNano(), State: "applied", MembershipCurrent: true,
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStatusResponse(raw); err != nil {
		t.Fatalf("valid status response: %v", err)
	}
	if _, err := DecodeStatusResponse(append(raw, bytes.Repeat([]byte(" "), 16<<10)...)); err == nil {
		t.Fatal("oversized status response accepted")
	}
	unknown := bytes.Replace(raw, []byte(`"state":"applied"`), []byte(`"state":"applied","unexpected":true`), 1)
	if bytes.Equal(unknown, raw) {
		t.Fatal("unknown-field fixture was not built")
	}
	if _, err := DecodeStatusResponse(unknown); err == nil {
		t.Fatal("unknown status response field accepted")
	}
}

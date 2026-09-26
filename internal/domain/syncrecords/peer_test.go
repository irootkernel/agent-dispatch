package syncrecords

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

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
